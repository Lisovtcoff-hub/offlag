// main.go — OffLag server (single global monthly price via settings + Admin UI)
// Правки: DB_PATH + WAL, UTC timestamps, PORT из ENV, аккуратный лог
// Добавлено: vpn_panels, синхронизация с 3x-ui через Python, миграция пользователей, простой rate limit на /send_code

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"

	yookassa "github.com/evzubkov/go-yookassa"
)

// ======================= GLOBALS =======================
var (
	db               *sql.DB
	jwtSecret        []byte
	smtpHost         string
	smtpPort         string
	smtpUser         string
	smtpPass         string
	adminEmails      map[string]bool
	adminUIPassword  string
	adminUISessions  = map[string]time.Time{} // token -> expiry
	adminUISessionMu sync.Mutex

	adminUISessionTTL = 24 * time.Hour

	accessTokenTTL  = 1 * time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour

	// default monthly price if DB empty
	envDefaultMonthly float64 = 60.0

	// ==== YooKassa (YooMoney) ====
	yooClient      *yookassa.Client
	yooShopID      string
	yooAPIKey      string
	yooReturnURL   string
	yooWebhookUser string
	yooWebhookPass string
	yooEnabled     bool

	pythonBin       string
	pythonScriptDir string
)

// ---- time helpers (UTC everywhere) ----
func now() time.Time                   { return time.Now().UTC() }
func nowStr() string                   { return now().Format(time.RFC3339) }
func addDurStr(d time.Duration) string { return now().Add(d).Format(time.RFC3339) }

func pythonCommand(script string) *exec.Cmd {
	return exec.Command(pythonBin, filepath.Join(pythonScriptDir, script))
}

func getSetting(key string) string {
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err == nil {
		return strings.TrimSpace(v)
	}
	return ""
}

func setSetting(key, value string) error {
	_, err := db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)
                       ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, nowStr())
	return err
}

func loadYooConfig() {
	// defaults from env
	yooShopID = strings.TrimSpace(os.Getenv("YOOKASSA_SHOP_ID"))
	yooAPIKey = strings.TrimSpace(os.Getenv("YOOKASSA_API_KEY"))
	yooReturnURL = strings.TrimSpace(os.Getenv("YOOKASSA_RETURN_URL"))
	yooWebhookUser = strings.TrimSpace(os.Getenv("YOOKASSA_WEBHOOK_USER"))
	yooWebhookPass = strings.TrimSpace(os.Getenv("YOOKASSA_WEBHOOK_PASS"))
	yooEnabled = true

	if v := getSetting("yookassa_enabled"); v != "" {
		yooEnabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := getSetting("yookassa_shop_id"); v != "" {
		yooShopID = v
	}
	if v := getSetting("yookassa_api_key"); v != "" {
		yooAPIKey = v
	}
	if v := getSetting("yookassa_return_url"); v != "" {
		yooReturnURL = v
	}
	if v := getSetting("yookassa_webhook_user"); v != "" {
		yooWebhookUser = v
	}
	if v := getSetting("yookassa_webhook_pass"); v != "" {
		yooWebhookPass = v
	}

	if yooReturnURL == "" {
		yooReturnURL = "https://offlag.app/payments/return"
	}

	if !yooEnabled || yooShopID == "" || yooAPIKey == "" {
		yooClient = nil
		if !yooEnabled {
			log.Println("⚠️ YooKassa отключена через админку")
		} else {
			log.Println("⚠️ YooKassa не настроена: YOOKASSA_SHOP_ID/YOOKASSA_API_KEY пустые — endpoints оплаты будут отвечать 503")
		}
		return
	}
	yooClient = yookassa.NewClient(yooShopID, yooAPIKey)
	log.Println("✅ YooKassa client инициализирован")
}

// ======================= MAIN ==========================
func main() {
	_ = godotenv.Load()

	// ENV
	if v := strings.TrimSpace(os.Getenv("DEFAULT_MONTHLY_PRICE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			envDefaultMonthly = f
		}
	}
	jwtSecret = []byte(os.Getenv("JWT_SECRET"))
	if len(jwtSecret) < 16 {
		log.Fatal("JWT_SECRET is too short; use a long random string")
	}
	smtpHost = os.Getenv("SMTP_HOST")
	smtpPort = os.Getenv("SMTP_PORT")
	smtpUser = os.Getenv("SMTP_USER")
	smtpPass = os.Getenv("SMTP_PASS")

	pythonBin = strings.TrimSpace(os.Getenv("PYTHON_BIN"))
	if pythonBin == "" {
		pythonBin = "python3"
	}
	pythonScriptDir = strings.TrimSpace(os.Getenv("PYTHON_SCRIPT_DIR"))
	if pythonScriptDir == "" {
		pythonScriptDir = "."
	}

	adminEmails = make(map[string]bool)
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			adminEmails[e] = true
		}
	}
	adminUIPassword = strings.TrimSpace(os.Getenv("ADMIN_UI_PASSWORD"))
	if len(adminUIPassword) < 16 {
		log.Println("⚠️ ADMIN_UI_PASSWORD is short; set a long random string in .env")
	}

	// DB path + WAL
	dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
	if dbPath == "" {
		dbPath = "./vpn.db"
	}
	absDBPath, _ := filepath.Abs(dbPath)
	// mattn/go-sqlite3 понимает эти параметры через DSN:
	// _foreign_keys=on, _busy_timeout=ms, _journal_mode=WAL
	dsn := absDBPath + "?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL"

	var err error
	db, err = sql.Open("sqlite3", dsn)
	if err != nil {
		log.Fatal(err)
	}
	initDB()
	loadYooConfig()

	// 🔁 Периодическое обновление онлайна панелей раз в 300 секунд
	go func() {
		// сразу один прогон при старте
		refreshPanelStatsFromPython()

		ticker := time.NewTicker(300 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			refreshPanelStatsFromPython()
		}
	}()

	// Fiber
	app := fiber.New(fiber.Config{
		Prefork:      false,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		BodyLimit:    2 * 1024 * 1024,
	})

	app.Use(recover.New())
	app.Use(func(c *fiber.Ctx) error {
		if isIPBanned(c.IP()) {
			return c.Status(403).SendString("banned")
		}
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "same-origin")
		c.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		return c.Next()
	})

	corsOrigins := strings.TrimSpace(os.Getenv("CORS_ALLOW_ORIGINS"))
	if corsOrigins == "" {
		corsOrigins = "*"
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins: corsOrigins,
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Authorization",
	}))

	app.Get("/health", func(c *fiber.Ctx) error {
		if err := db.Ping(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "error"})
		}
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// ---- Admin UI (пароль из .env, без JWT) ----
	initAdminUI(app)

	// ---- Public API ----
	// Добавлен простой rate-limit по IP: не более 5 запросов в минуту на /send_code
	app.Post("/send_code",
		limiter.New(limiter.Config{
			Max:        5,
			Expiration: 1 * time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return c.IP()
			},
		}),
		sendCode,
	)
	app.Post("/verify_code",
		limiter.New(limiter.Config{
			Max:        10,
			Expiration: 1 * time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return c.IP()
			},
		}),
		verifyCode,
	)
	app.Post("/set_nickname", authMiddleware, setNickname)
	app.Post("/auth/refresh", refreshAuth)
	app.Get("/app/version", getAppVersion)

	// ---- Auth API ----
	app.Get("/profile", authMiddleware, getProfile)
	app.Post("/logout_device", authMiddleware, logoutCurrent)
	app.Post("/logout_all", authMiddleware, logoutAll)

	// promo redeem
	app.Post("/redeem_promo", authMiddleware, redeemPromo)
	app.Post("/premium/activate", authMiddleware, premiumActivate)
	app.Get("/announcements/next", authMiddleware, getAnnouncementNext)
	app.Post("/announcements/read", authMiddleware, markAnnouncementRead)

	// email change
	app.Post("/change_email_request", authMiddleware, changeEmailRequest)
	app.Post("/change_email_confirm", authMiddleware, changeEmailConfirm)

	// ---- VPN API ----
	app.Get("/vpn/nodes", authMiddleware, getVpnNodes)

	// ---- Payment API (YooKassa) ----
	app.Post("/payments/yookassa/create",
		limiter.New(limiter.Config{
			Max:        10,
			Expiration: 1 * time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return c.IP()
			},
		}),
		authMiddleware,
		createYooKassaPayment,
	)
	app.Post("/payments/yookassa/webhook", yooKassaWebhook) // вебхук, защищён Basic Auth внутри

	// ---- Admin API (JWT + ADMIN_EMAILS) ----
	admin := app.Group("/admin", authMiddleware, adminMiddleware)
	admin.Post("/set_user_price", adminSetUserPrice)
	admin.Post("/settings/set_monthly_price", adminSetMonthlyPrice)
	admin.Get("/settings/get_monthly_price", adminGetMonthlyPrice)
	admin.Post("/promo_create", adminPromoCreate)

	// background cleanup
	go cleanupJob()

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	log.Printf("🚀 OffLag server starting on :%s (DB=%s)\n", port, absDBPath)
	log.Fatal(app.Listen(":" + port))
}

// ======================= DB INIT =======================
func initDB() {
	schema := `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	email TEXT UNIQUE NOT NULL,
	nickname TEXT UNIQUE,
	balance REAL NOT NULL DEFAULT 0,
	plan_id INTEGER,
	price_override REAL,
	blocked INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME,
	last_login DATETIME
);

CREATE TABLE IF NOT EXISTS tariffs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	code TEXT UNIQUE NOT NULL,
	name TEXT NOT NULL,
	monthly_price REAL NOT NULL,
	is_active INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME
);

CREATE TABLE IF NOT EXISTS devices (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	device_name TEXT,
	user_agent TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_seen_at DATETIME,
	UNIQUE(user_id, device_name),
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	device_id INTEGER,
	token TEXT UNIQUE NOT NULL,
	ip TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL,
	revoked_at DATETIME,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS refresh_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	device_id INTEGER,
	token_hash TEXT UNIQUE NOT NULL,
	ip TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL,
	revoked_at DATETIME,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_expires ON refresh_tokens(expires_at);

CREATE TABLE IF NOT EXISTS email_codes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	email TEXT NOT NULL,
	code TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_codes_email ON email_codes(email);

CREATE TABLE IF NOT EXISTS auth_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER,
	email TEXT,
	device_id INTEGER,
	device TEXT,
	ip TEXT,
	event TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL,
	FOREIGN KEY(device_id) REFERENCES devices(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS pending_email_changes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	old_email TEXT NOT NULL,
	new_email TEXT NOT NULL,
	code TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL,
	UNIQUE(user_id),
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS email_change_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER,
	old_email TEXT,
	new_email TEXT,
	ip TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS promo_codes (
	code TEXT PRIMARY KEY,
	amount REAL NOT NULL,
	max_uses INTEGER NOT NULL DEFAULT 1,
	used_count INTEGER NOT NULL DEFAULT 0,
	expires_at DATETIME,
	created_by TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS promo_redemptions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	code TEXT NOT NULL,
	user_id INTEGER NOT NULL,
	amount REAL NOT NULL,
	redeemed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(code, user_id),
	FOREIGN KEY(code) REFERENCES promo_codes(code) ON DELETE CASCADE,
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS payments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	provider TEXT NOT NULL,
	provider_payment_id TEXT NOT NULL,
	amount REAL NOT NULL,
	currency TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	paid_at DATETIME,
	raw TEXT,
	UNIQUE(provider, provider_payment_id),
	FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS vpn_panels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    country TEXT NOT NULL,
    base_url TEXT NOT NULL,
    login TEXT NOT NULL,
    password TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,

    -- приоритет и метаданные
    priority INTEGER NOT NULL DEFAULT 100,
    region   TEXT NOT NULL DEFAULT 'EU',
    role     TEXT NOT NULL DEFAULT 'general',

    -- premium override
    premium_override INTEGER NOT NULL DEFAULT 0,
    premium_until DATETIME,

    -- VLESS / Reality параметры для этой панели
    vless_server      TEXT, -- host / IP
    vless_public_key  TEXT,
    vless_short_id    TEXT,

    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME
);


CREATE TABLE IF NOT EXISTS vpn_panel_stats (
    panel_id     INTEGER PRIMARY KEY,
    online_users INTEGER NOT NULL DEFAULT 0,
    total_users  INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL,
    FOREIGN KEY(panel_id) REFERENCES vpn_panels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS vpn_panel_stats_daily (
    panel_id     INTEGER NOT NULL,
    day          TEXT NOT NULL, -- YYYY-MM-DD
    online_users INTEGER NOT NULL DEFAULT 0,
    total_users  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(panel_id, day),
    FOREIGN KEY(panel_id) REFERENCES vpn_panels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS premium_codes (
    code TEXT PRIMARY KEY,
    panel_id INTEGER NOT NULL,
    max_users INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    premium_until DATETIME NOT NULL,
    created_by TEXT,
    FOREIGN KEY(panel_id) REFERENCES vpn_panels(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_premium_codes_panel ON premium_codes(panel_id);
CREATE INDEX IF NOT EXISTS idx_premium_codes_exp ON premium_codes(expires_at);

CREATE TABLE IF NOT EXISTS premium_memberships (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    activated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    UNIQUE(code, user_id),
    FOREIGN KEY(code) REFERENCES premium_codes(code) ON DELETE CASCADE,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_premium_memberships_user ON premium_memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_premium_memberships_exp ON premium_memberships(expires_at);

CREATE TABLE IF NOT EXISTS announcements (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    image_url TEXT,
    cta_url TEXT,
    active_until DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_announcements_created ON announcements(created_at);

CREATE TABLE IF NOT EXISTS announcement_reads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    announcement_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    read_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(announcement_id, user_id),
    FOREIGN KEY(announcement_id) REFERENCES announcements(id) ON DELETE CASCADE,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_announcement_reads_user ON announcement_reads(user_id);

CREATE TABLE IF NOT EXISTS app_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT NOT NULL,
    version_code INTEGER NOT NULL,
    version_name TEXT,
    min_required INTEGER NOT NULL DEFAULT 0,
    message TEXT,
    url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_app_versions_platform ON app_versions(platform, version_code);

CREATE TABLE IF NOT EXISTS ip_bans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip TEXT NOT NULL,
    reason TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ip_bans_ip ON ip_bans(ip);

`
	if _, err := db.Exec(schema); err != nil {
		log.Fatal(err)
	}

	// лёгкая миграция для старых БД: пытаемся добавить новые колонки, ошибки игнорируем
	_, _ = db.Exec(`ALTER TABLE vpn_panel_stats ADD COLUMN total_users INTEGER NOT NULL DEFAULT 0`)

	// новые поля в vpn_panels (если БД старая — добавятся, если новые уже есть — ошибка игнорируется)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN priority INTEGER NOT NULL DEFAULT 100`)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN region TEXT NOT NULL DEFAULT 'EU'`)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN role TEXT NOT NULL DEFAULT 'general'`)

	// premium override fields
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN premium_override INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN premium_until DATETIME`)

	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN vless_server TEXT`)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN vless_public_key TEXT`)
	_, _ = db.Exec(`ALTER TABLE vpn_panels ADD COLUMN vless_short_id TEXT`)

	// seed global monthly price if empty
	var cnt int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key='monthly_price'`).Scan(&cnt)
	if cnt == 0 {
		_, _ = db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('monthly_price',?,?)`,
			strconv.FormatFloat(envDefaultMonthly, 'f', -1, 64),
			nowStr(),
		)
	}

	// blocked flag for users
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0`)
}

// ===================== BRAND EMAIL BUILDER =====================

const (
	brandBg     = "#1F1F1F" // kBg
	brandCard   = "#2B2B2B" // kSurface
	brandBorder = "#3D3D3D" // kBorder
	brandText   = "#FFFFFF" // kInk
	brandMuted  = "#B9B9B9"
	brandAccent = "#FFFFFF"
)

func buildBrandEmailHTML(preheader, title, lead, htmlBlock, btnLabel, btnURL string) string {
	button := ""
	if strings.TrimSpace(btnURL) != "" && strings.TrimSpace(btnLabel) != "" {
		button = fmt.Sprintf(`
        <table role="presentation" cellspacing="0" cellpadding="0" style="margin:24px 0 0 0;">
          <tr>
            <td align="center" style="border-radius:999px;border:1px solid %[1]s;">
              <a href="%[2]s" target="_blank" 
                 style="display:inline-block;padding:12px 18px;color:%[3]s;background:transparent;border-radius:999px;
                        text-decoration:none;font-weight:700;font-family:Segoe UI,Roboto,Arial,sans-serif;">
                %[4]s
              </a>
            </td>
          </tr>
        </table>`, brandBorder, btnURL, brandText, htmlEscape(btnLabel))
	}
	preheaderSpan := ""
	if preheader != "" {
		preheaderSpan = `<div style="display:none;opacity:0;visibility:hidden;overflow:hidden;height:0;width:0;
            font-size:1px;line-height:1px;color:transparent;">` + htmlEscape(preheader) + `</div>`
	}
	logo := `
      <div style="display:inline-block;padding:8px 14px;border-radius:16px;border:1px solid ` + brandBorder + `;
                  font-weight:800;color:` + brandText + `;background:` + brandCard + `;font-family:Segoe UI,Roboto,Arial,sans-serif;">
        OFF<span style="opacity:.9">LAG</span>
      </div>`

	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%[1]s</title></head>
<body style="margin:0;background:%[2]s;">
  %[3]s
  <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" 
         style="background:%[2]s;padding:24px 0;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" 
               style="max-width:560px;width:92%%;">
          <tr>
            <td align="left" style="padding:0 0 12px 0;">%[4]s</td>
          </tr>
          <tr>
            <td style="background:%[5]s;border:1px solid %[6]s;border-radius:16px;padding:24px;">
              <h1 style="margin:0 0 8px 0;font-size:22px;color:%[7]s;font-family:Segoe UI,Roboto,Arial,sans-serif;">%[1]s</h1>
              <p style="margin:0 0 18px 0;color:%[8]s;font-size:14px;line-height:1.6;font-family:Segoe UI,Roboto,Arial,sans-serif;">%[9]s</p>
              %[10]s
              %[11]s
              <hr style="border:0;border-top:1px solid %[6]s;margin:22px 0;">
              <p style="margin:0;color:%[8]s;font-size:12px;line-height:1.6;font-family:Segoe UI,Roboto,Arial,sans-serif;">
                Если это были не вы — проигнорируйте письмо или напишите в&nbsp;поддержку:
                <a href="mailto:support@offlag.app" style="color:%[7]s;text-decoration:none;font-weight:700;">support@offlag.app</a>
              </p>
            </td>
          </tr>
          <tr>
            <td align="center" style="padding:14px 0 0 0;color:%[8]s;font-size:12px;font-family:Segoe UI,Roboto,Arial,sans-serif;">
              © %[12]d OffLag. Все права защищены.
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body></html>`,
		htmlEscape(title),
		brandBg, preheaderSpan, logo,
		brandCard, brandBorder, brandText, brandMuted,
		lead, htmlBlock, button, now().Year(),
	)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

func buildCodeBox(label, value string) string {
	value = htmlEscape(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	lbl := ""
	if strings.TrimSpace(label) != "" {
		lbl = fmt.Sprintf(`<div style="color:%s;font-size:12px;margin-bottom:6px;">%s</div>`, brandMuted, htmlEscape(label))
	}
	return fmt.Sprintf(`
      <div style="margin:12px 0 0 0;">
        %s
        <div style="padding:14px 16px;border:1px dashed %s;border-radius:12px;
                    background:%s;display:inline-block;font-family:ui-monospace,Consolas,monospace;
                    font-size:22px;font-weight:800;letter-spacing:3px;color:%s;">
          %s
        </div>
      </div>`, lbl, brandBorder, brandCard, brandText, value)
}

// ======================= HELPERS =======================
func rawAuthToken(c *fiber.Ctx) string {
	h := strings.TrimSpace(c.Get("Authorization"))
	if h == "" {
		return ""
	}
	l := strings.ToLower(h)
	if strings.HasPrefix(l, "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}
func normEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func clampDeviceName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		return s[:64]
	}
	if s == "" {
		return "UnknownDevice"
	}
	return s
}

func isUserBlocked(userID int64) bool {
	var blocked int
	_ = db.QueryRow("SELECT blocked FROM users WHERE id=?", userID).Scan(&blocked)
	return blocked == 1
}

func isEmailBlocked(email string) bool {
	var blocked int
	_ = db.QueryRow("SELECT blocked FROM users WHERE email=?", normEmail(email)).Scan(&blocked)
	return blocked == 1
}

// keep only N newest active sessions
func enforceMaxSessions(userID int64, max int) {
	if max <= 0 {
		return
	}
	db.Exec(`
        UPDATE sessions
        SET revoked_at=?
        WHERE id IN (
          SELECT id FROM sessions
          WHERE user_id=? AND revoked_at IS NULL AND expires_at>?
          ORDER BY created_at DESC
          LIMIT -1 OFFSET ?
        )
    `, nowStr(), userID, nowStr(), max)
}

func isIPBanned(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	nowS := nowStr()
	var cnt int
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM ip_bans WHERE ip=? AND (expires_at IS NULL OR expires_at > ?)`,
		ip, nowS,
	).Scan(&cnt)
	return cnt > 0
}
func strOr(s sql.NullString, def string) string {
	if s.Valid {
		return s.String
	}
	return def
}
func nullFloat(f sql.NullFloat64) *float64 {
	if f.Valid {
		v := f.Float64
		return &v
	}
	return nil
}
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func defBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
func nullableStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// ======================= VPN PANELS ====================

const defaultVlessPort = 443

type VPNPanel struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Country  string `json:"country"`
	BaseURL  string `json:"base_url"`
	Login    string `json:"login"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`

	Priority int    `json:"priority"` // 0 — самый приоритетный
	Region   string `json:"region"`   // EU, RU, ASIA...
	Role     string `json:"role"`     // general / gaming / streaming и т.п.

	PremiumOverride bool   `json:"premium_override"`
	PremiumUntil    string `json:"premium_until"`

	// VLESS / Reality
	VlessServer    string `json:"vless_server"`
	VlessPublicKey string `json:"vless_public_key"`
	VlessShortID   string `json:"vless_short_id"`
}

func getActivePanels() ([]VPNPanel, error) {
	nowS := nowStr()
	rows, err := db.Query(`
        SELECT
            id,
            name,
            country,
            base_url,
            login,
            password,
            enabled,
            priority,
            region,
            role,
            premium_override,
            COALESCE(premium_until,''),
            vless_server,
            vless_public_key,
            vless_short_id
        FROM vpn_panels
        WHERE enabled = 1
          AND (premium_override = 0 OR premium_until IS NULL OR premium_until <= ?)
    `, nowS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []VPNPanel
	for rows.Next() {
		var p VPNPanel
		var enabledInt int
		var premiumInt int
		if err := rows.Scan(
			&p.ID,
			&p.Name,
			&p.Country,
			&p.BaseURL,
			&p.Login,
			&p.Password,
			&enabledInt,
			&p.Priority,
			&p.Region,
			&p.Role,
			&premiumInt,
			&p.PremiumUntil,
			&p.VlessServer,
			&p.VlessPublicKey,
			&p.VlessShortID,
		); err != nil {
			return nil, err
		}
		p.Enabled = enabledInt == 1
		p.PremiumOverride = premiumInt == 1
		res = append(res, p)
	}
	return res, nil
}

func getPanelByID(id int64) (*VPNPanel, error) {
	var p VPNPanel
	var enabledInt int
	var premiumInt int
	err := db.QueryRow(`
        SELECT
            id,
            name,
            country,
            base_url,
            login,
            password,
            enabled,
            priority,
            region,
            role,
            premium_override,
            COALESCE(premium_until,''),
            vless_server,
            vless_public_key,
            vless_short_id
        FROM vpn_panels
        WHERE id = ?
    `, id).Scan(
		&p.ID,
		&p.Name,
		&p.Country,
		&p.BaseURL,
		&p.Login,
		&p.Password,
		&enabledInt,
		&p.Priority,
		&p.Region,
		&p.Role,
		&premiumInt,
		&p.PremiumUntil,
		&p.VlessServer,
		&p.VlessPublicKey,
		&p.VlessShortID,
	)

	if err != nil {
		return nil, err
	}
	p.Enabled = enabledInt == 1
	p.PremiumOverride = premiumInt == 1
	return &p, nil
}

func getPremiumPanelForUser(userID int64) (*VPNPanel, error) {
	nowS := nowStr()
	var p VPNPanel
	var enabledInt int
	var premiumInt int
	err := db.QueryRow(`
        SELECT
            p.id,
            p.name,
            p.country,
            p.base_url,
            p.login,
            p.password,
            p.enabled,
            p.priority,
            p.region,
            p.role,
            p.premium_override,
            COALESCE(p.premium_until,''),
            p.vless_server,
            p.vless_public_key,
            p.vless_short_id
        FROM premium_memberships m
        JOIN premium_codes c ON c.code = m.code
        JOIN vpn_panels p ON p.id = c.panel_id
        WHERE m.user_id = ?
          AND m.expires_at > ?
          AND c.premium_until > ?
          AND p.enabled = 1
        ORDER BY m.expires_at DESC
        LIMIT 1
    `, userID, nowS, nowS).Scan(
		&p.ID,
		&p.Name,
		&p.Country,
		&p.BaseURL,
		&p.Login,
		&p.Password,
		&enabledInt,
		&p.Priority,
		&p.Region,
		&p.Role,
		&premiumInt,
		&p.PremiumUntil,
		&p.VlessServer,
		&p.VlessPublicKey,
		&p.VlessShortID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Enabled = enabledInt == 1
	p.PremiumOverride = premiumInt == 1
	return &p, nil
}

type premiumInfo struct {
	Active    bool
	UsersLeft int
	DaysLeft  int
	ExpiresAt string
	PanelID   int64
}

func getPremiumInfoForUser(userID int64) premiumInfo {
	nowS := nowStr()
	var (
		code         string
		panelID      int64
		maxUsers     int
		premiumUntil string
		expiresAt    string
	)
	err := db.QueryRow(`
        SELECT m.code, c.panel_id, c.max_users, c.premium_until, m.expires_at
        FROM premium_memberships m
        JOIN premium_codes c ON c.code = m.code
        WHERE m.user_id = ?
          AND m.expires_at > ?
          AND c.premium_until > ?
        ORDER BY m.expires_at DESC
        LIMIT 1
    `, userID, nowS, nowS).Scan(&code, &panelID, &maxUsers, &premiumUntil, &expiresAt)
	if err != nil {
		return premiumInfo{Active: false}
	}

	var activeUsers int
	_ = db.QueryRow(`SELECT COUNT(*) FROM premium_memberships WHERE code=? AND expires_at>?`, code, nowS).Scan(&activeUsers)
	usersLeft := maxUsers - activeUsers
	if usersLeft < 0 {
		usersLeft = 0
	}

	daysLeft := 0
	if t, e := time.Parse(time.RFC3339, expiresAt); e == nil {
		daysLeft = int(math.Ceil(time.Until(t).Hours() / 24.0))
		if daysLeft < 0 {
			daysLeft = 0
		}
	}

	return premiumInfo{
		Active:    true,
		UsersLeft: usersLeft,
		DaysLeft:  daysLeft,
		ExpiresAt: expiresAt,
		PanelID:   panelID,
	}
}

// то, что мы отправляем в Python
type syncUser struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

type syncPayload struct {
	Mode   string     `json:"mode"`
	Users  []syncUser `json:"users"`
	Panels []VPNPanel `json:"panels"`
}

// низкоуровневый вызов python3 sync_3xui.py
func call3xUISync(mode string, panels []VPNPanel, users []syncUser) {
	if len(panels) == 0 || len(users) == 0 {
		return
	}
	pl := syncPayload{
		Mode:   mode,
		Users:  users,
		Panels: panels,
	}
	data, err := json.Marshal(pl)
	if err != nil {
		log.Println("sync3xui marshal error:", err)
		return
	}

	cmd := pythonCommand("sync_3xui.py")
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("sync_3xui.py error: %v, out=%s\n", err, string(out))
	} else {
		log.Printf("sync_3xui.py ok: %s\n", string(out))
	}
}

// Внутренний тип для карты статистики по панелям
type panelStats struct {
	Online int
	Total  int
}

// collectVPNStats вызывает stats_3xui.py и возвращает карту panelID -> (online,total).
// При любой ошибке просто логируем и возвращаем пустую карту, чтобы API не падал.
func collectVPNStats(panels []VPNPanel) map[int64]panelStats {
	res := make(map[int64]panelStats)
	if len(panels) == 0 {
		return res
	}

	// Готовим payload: {"panels":[ ... VPNPanel ... ]}
	payload := struct {
		Panels []VPNPanel `json:"panels"`
	}{
		Panels: panels,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Println("stats_3xui marshal error:", err)
		return res
	}

	cmd := pythonCommand("stats_3xui.py")
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("stats_3xui.py error: %v, out=%s\n", err, string(out))
		return res
	}

	// Разбираем ответ скрипта
	var resp struct {
		Ok     bool `json:"ok"`
		Panels []struct {
			PanelID int64 `json:"panel_id"`
			Online  int   `json:"online"`
			Total   int   `json:"total"`
		} `json:"panels"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		log.Println("stats_3xui json unmarshal error:", err)
		return res
	}

	for _, p := range resp.Panels {
		res[p.PanelID] = panelStats{
			Online: p.Online,
			Total:  p.Total,
		}
	}

	return res
}

// миграция всех существующих пользователей на одну панель
func syncAllUsersToPanel(panelID int64) error {
	panel, err := getPanelByID(panelID)
	if err != nil {
		return err
	}

	rows, err := db.Query(`SELECT id, email, COALESCE(nickname, '') FROM users`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var users []syncUser
	for rows.Next() {
		var u syncUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Nickname); err != nil {
			return err
		}
		users = append(users, u)
	}

	// не блокируем HTTP-запрос — уходим в горутину
	go call3xUISync("sync_panel", []VPNPanel{*panel}, users)
	return nil
}

// синхронизация одного нового пользователя во все активные панели
func syncNewUserToAllPanels(userID int64, email, nickname string) {
	panels, err := getActivePanels()
	if err != nil {
		log.Println("getActivePanels error:", err)
		return
	}
	if len(panels) == 0 {
		return
	}
	u := syncUser{ID: userID, Email: email, Nickname: nickname}
	go call3xUISync("new_user", panels, []syncUser{u})
}

func syncUserToPanel(userID int64, panelID int64) {
	panel, err := getPanelByID(panelID)
	if err != nil {
		log.Println("getPanelByID error:", err)
		return
	}
	var u syncUser
	if err := db.QueryRow(`SELECT id, email, COALESCE(nickname,'') FROM users WHERE id=?`, userID).
		Scan(&u.ID, &u.Email, &u.Nickname); err != nil {
		log.Println("syncUserToPanel user query error:", err)
		return
	}
	go call3xUISync("new_user", []VPNPanel{*panel}, []syncUser{u})
}

// периодическое обновление статистики панелей (онлайн-пользователи) через Python
// периодическое обновление статистики панелей (онлайн-пользователи) через Python
// периодическое обновление статистики панелей (онлайн-пользователи) через Python
func refreshPanelStatsFromPython() {
	panels, err := getActivePanels()
	if err != nil {
		log.Println("panel stats: getActivePanels error:", err)
		return
	}
	if len(panels) == 0 {
		// нет включённых панелей — нечего опрашивать
		return
	}

	// полезная нагрузка для stats_3xui.py
	type payload struct {
		Mode   string     `json:"mode"`
		Panels []VPNPanel `json:"panels"`
	}

	pl := payload{
		Mode:   "stats",
		Panels: panels,
	}

	data, err := json.Marshal(pl)
	if err != nil {
		log.Println("panel stats: marshal error:", err)
		return
	}

	cmd := pythonCommand("stats_3xui.py")
	cmd.Stdin = bytes.NewReader(data)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("panel stats: python error: %v, out=%s\n", err, string(out))
		return
	}

	// ожидаемый ответ от stats_3xui.py:
	// {
	//   "ok": true,
	//   "panels": [
	//     { "panel_id": 1, "name": "nl-test", "online": 23, "total": 42, "errors": [] }
	//   ]
	// }
	type panelResp struct {
		PanelID int64    `json:"panel_id"`
		Name    string   `json:"name"`
		Online  int      `json:"online"`
		Total   int      `json:"total"`
		Errors  []string `json:"errors"`
	}
	type resp struct {
		Ok     bool        `json:"ok"`
		Panels []panelResp `json:"panels"`
		Error  string      `json:"error"`
	}

	var r resp
	if err := json.Unmarshal(out, &r); err != nil {
		log.Println("panel stats: unmarshal error:", err)
		return
	}

	nowS := nowStr()

	tx, err := db.Begin()
	if err != nil {
		log.Println("panel stats: tx begin error:", err)
		return
	}
	defer tx.Rollback()

	for _, p := range r.Panels {
		if _, err := tx.Exec(`
            INSERT INTO vpn_panel_stats(panel_id, online_users, total_users, updated_at)
            VALUES(?,?,?,?)
            ON CONFLICT(panel_id) DO UPDATE SET
                online_users = excluded.online_users,
                total_users  = excluded.total_users,
                updated_at   = excluded.updated_at
        `, p.PanelID, p.Online, p.Total, nowS); err != nil {
			log.Println("panel stats: upsert error:", err)
		}

		day := now().Format("2006-01-02")
		if _, err := tx.Exec(`
            INSERT INTO vpn_panel_stats_daily(panel_id, day, online_users, total_users)
            VALUES(?,?,?,?)
            ON CONFLICT(panel_id, day) DO UPDATE SET
                online_users = excluded.online_users,
                total_users  = excluded.total_users
        `, p.PanelID, day, p.Online, p.Total); err != nil {
			log.Println("panel stats daily upsert error:", err)
		}

		if len(p.Errors) > 0 {
			log.Printf("panel stats: panel_id=%d errors=%v\n", p.PanelID, p.Errors)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Println("panel stats: tx commit error:", err)
		return
	}
}

// ======================= VPN PANELS STATS (online) ===========

// Формат ответа от stats_3xui.py для одной панели.
type panelOnlineStat struct {
	PanelID int64 `json:"panel_id"`
	Online  int   `json:"online"`
}

// Вызывает Python-скрипт stats_3xui.py и возвращает карту panel_id -> online.
func getPanelsOnline(panels []VPNPanel) map[int64]int {
	result := make(map[int64]int)
	if len(panels) == 0 {
		return result
	}

	// Готовим минимальный JSON: mode + список панелей.
	payload := struct {
		Mode   string     `json:"mode"`
		Panels []VPNPanel `json:"panels"`
	}{
		Mode:   "stats",
		Panels: panels,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Println("stats_3xui marshal error:", err)
		return result
	}

	cmd := pythonCommand("stats_3xui.py")
	cmd.Stdin = strings.NewReader(string(data))

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("stats_3xui.py error: %v, out=%s\n", err, string(out))
		return result
	}

	var resp struct {
		OK     bool              `json:"ok"`
		Panels []panelOnlineStat `json:"panels"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		log.Printf("stats_3xui.py unmarshal error: %v, out=%s\n", err, string(out))
		return result
	}

	for _, p := range resp.Panels {
		if p.PanelID == 0 {
			continue
		}
		if p.Online < 0 {
			p.Online = 0
		}
		result[p.PanelID] = p.Online
	}
	return result
}

// ======================= UUID FETCH VIA PYTHON ===============

// Использует get_uuid_3xui.py для получения VLESS-UUID пользователя на каждой панели.
// На вход отправляем email + список панелей (как есть), на выход — карта panel_id -> uuid.
func fetchPanelUUIDsForUser(email string, panels []VPNPanel) map[int64]string {
	res := make(map[int64]string)
	email = normEmail(email)
	if email == "" || len(panels) == 0 {
		return res
	}

	payload := struct {
		Email  string     `json:"email"`
		Panels []VPNPanel `json:"panels"`
	}{
		Email:  email,
		Panels: panels,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Println("get_uuid_3xui marshal error:", err)
		return res
	}

	cmd := pythonCommand("get_uuid_3xui.py")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("get_uuid_3xui.py error: %v, out=%s\n", err, string(out))
		return res
	}

	var resp struct {
		Ok      bool `json:"ok"`
		Results []struct {
			PanelID int64  `json:"panel_id"`
			Found   bool   `json:"found"`
			UUID    string `json:"uuid"`
			Error   string `json:"error"`
		} `json:"results"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		log.Println("get_uuid_3xui unmarshal error:", err)
		return res
	}

	for _, r := range resp.Results {
		if r.Error != "" {
			log.Printf("get_uuid_3xui panel_id=%d error=%s\n", r.PanelID, r.Error)
		}
		if r.Found && strings.TrimSpace(r.UUID) != "" {
			res[r.PanelID] = strings.TrimSpace(r.UUID)
		}
	}

	return res
}

// ======================= VPN NODES API ====================

// То, что будем отдавать клиенту (Flutter)
type VpnNodeDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Country  string `json:"country"`
	BaseURL  string `json:"base_url"`
	Online   int    `json:"online"`
	Total    int    `json:"total"`
	Priority int    `json:"priority"`

	// старые поля (для совместимости)
	VlessServer    string `json:"vless_server"`
	VlessUUID      string `json:"vless_uuid"`
	VlessPublicKey string `json:"vless_public_key"`
	VlessShortID   string `json:"vless_short_id"`

	// новые поля, чтобы во Flutter прилетало ровно то, что удобно:
	// serverHost, serverPort, uuid, publicKey, shortId.
	ServerHost string `json:"serverHost"`
	ServerPort int    `json:"serverPort"`
	UUID       string `json:"uuid"`
	PublicKey  string `json:"publicKey"`
	ShortID    string `json:"shortId"`
	Premium    bool   `json:"premium"`

	// совместимость со snake_case
	ServerHostCompat string `json:"server_host"`
	ServerPortCompat int    `json:"server_port"`
	PublicKeyCompat  string `json:"public_key"`
	ShortIDCompat    string `json:"short_id"`
}

// /vpn/nodes — список доступных серверов.
// Онлайн/total берём из кэша в таблице vpn_panel_stats.
// UUID пользователя берём через Python-скрипт get_uuid_3xui.py по email.
func getVpnNodes(c *fiber.Ctx) error {
	emailAny := c.Locals("email")
	email, _ := emailAny.(string)
	email = normEmail(email)

	includeAll := strings.TrimSpace(c.Query("all")) == "1"
	var panels []VPNPanel
	var premiumPanelID int64

	if userIDAny := c.Locals("user_id"); userIDAny != nil {
		if userID, ok := userIDAny.(int64); ok && userID > 0 {
			if premiumPanel, err := getPremiumPanelForUser(userID); err != nil {
				log.Println("getVpnNodes getPremiumPanelForUser error:", err)
			} else if premiumPanel != nil {
				premiumPanelID = premiumPanel.ID
				if includeAll {
					// premium user: allow all non-premium + their premium panel
					allPanels, err := getActivePanels()
					if err != nil {
						log.Println("getVpnNodes getActivePanels error:", err)
					} else {
						panels = allPanels
						found := false
						for _, p := range panels {
							if p.ID == premiumPanel.ID {
								found = true
								break
							}
						}
						if !found {
							panels = append([]VPNPanel{*premiumPanel}, panels...)
						}
					}
				} else {
					panels = []VPNPanel{*premiumPanel}
				}
			}
		}
	}

	if panels == nil {
		// Берём все enabled-панели из БД (без активных premium)
		var err error
		panels, err = getActivePanels()
		if err != nil {
			log.Println("getVpnNodes getActivePanels error:", err)
			return c.Status(500).JSON(fiber.Map{"error": "db error"})
		}
	}

	// Читаем кэш статистики из vpn_panel_stats
	statsMap := make(map[int64]panelStats)

	rows, err := db.Query(`
        SELECT panel_id, online_users, total_users
        FROM vpn_panel_stats
    `)
	if err != nil {
		log.Println("getVpnNodes stats query error:", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var (
				id     int64
				online int
				total  int
			)
			if err := rows.Scan(&id, &online, &total); err != nil {
				log.Println("getVpnNodes stats scan error:", err)
				continue
			}
			statsMap[id] = panelStats{
				Online: online,
				Total:  total,
			}
		}
	}

	// Получаем UUID пользователя на каждой панели через Python
	uuidMap := fetchPanelUUIDsForUser(email, panels)

	nodes := make([]VpnNodeDTO, 0, len(panels))
	for _, p := range panels {
		st := statsMap[p.ID] // если не нашли — будет zero-value (0,0)
		uuid := uuidMap[p.ID]
		serverHost := p.VlessServer
		serverPort := defaultVlessPort

		nodes = append(nodes, VpnNodeDTO{
			ID:       p.ID,
			Name:     p.Name,
			Country:  p.Country,
			BaseURL:  p.BaseURL,
			Online:   st.Online,
			Total:    st.Total,
			Priority: p.Priority,

			VlessServer:    p.VlessServer,
			VlessUUID:      uuid,
			VlessPublicKey: p.VlessPublicKey,
			VlessShortID:   p.VlessShortID,

			ServerHost: serverHost,
			ServerPort: serverPort,
			UUID:       uuid,
			PublicKey:  p.VlessPublicKey,
			ShortID:    p.VlessShortID,
			Premium:    premiumPanelID > 0 && p.ID == premiumPanelID,

			ServerHostCompat: serverHost,
			ServerPortCompat: serverPort,
			PublicKeyCompat:  p.VlessPublicKey,
			ShortIDCompat:    p.VlessShortID,
		})
	}

	return c.JSON(nodes)
}

// Basic Auth helper (для вебхука YooKassa)
func checkBasicAuth(c *fiber.Ctx, user, pass string) bool {
	if strings.TrimSpace(user) == "" {
		// если не настроено — не проверяем (для dev), но в проде лучше всегда задавать
		return true
	}
	h := strings.TrimSpace(c.Get("Authorization"))
	if h == "" || !strings.HasPrefix(h, "Basic ") {
		return false
	}

	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[6:]))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(payload), ":", 2)
	if len(parts) != 2 {
		return false
	}
	return parts[0] == user && parts[1] == pass
}

func randomDigits(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%10]
	}
	return string(b)
}
func genPromoCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return fmt.Sprintf("%s-%s-%s-%s", string(b[0:4]), string(b[4:8]), string(b[8:12]), string(b[12:16]))
}

// ============= GLOBAL MONTHLY PRICE (settings) =========
func getGlobalMonthlyPrice() float64 {
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='monthly_price'`).Scan(&v); err == nil {
		if f, e := strconv.ParseFloat(strings.TrimSpace(v), 64); e == nil && f > 0 {
			return f
		}
	}
	return envDefaultMonthly
}
func setGlobalMonthlyPrice(f float64) error {
	if f <= 0 {
		return fmt.Errorf("monthly_price must be > 0")
	}
	_, err := db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('monthly_price',?,?)
                       ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		strconv.FormatFloat(f, 'f', -1, 64), nowStr())
	return err
}

// ======================= ADMIN UI (PASSWORD) ===========
func initAdminUI(app *fiber.App) {
	ui := app.Group("/admin-ui")

	ui.Get("/_login", func(c *fiber.Ctx) error {
		return c.Type("html", "utf-8").SendString(loginHTML(""))
	})

	ui.Post("/_login",
		limiter.New(limiter.Config{
			Max:        10,
			Expiration: 1 * time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return c.IP()
			},
		}),
		func(c *fiber.Ctx) error {
			pass := strings.TrimSpace(c.FormValue("password"))
			if pass == "" {
				return c.Type("html", "utf-8").SendString(loginHTML("Введите пароль"))
			}
			if adminUIPassword == "" || pass != adminUIPassword {
				return c.Type("html", "utf-8").SendString(loginHTML("Неверный пароль"))
			}

			token := genPromoCode()
			adminUISessionMu.Lock()
			adminUISessions[token] = now().Add(adminUISessionTTL)
			adminUISessionMu.Unlock()
			c.Cookie(&fiber.Cookie{
				Name:     "admin_ui",
				Value:    token,
				HTTPOnly: true,
				SameSite: "Strict",
				Secure:   c.Protocol() == "https",
				Path:     "/",
				MaxAge:   int(adminUISessionTTL.Seconds()),
			})
			return c.Redirect("/admin-ui", fiber.StatusFound)
		},
	)

	auth := func(c *fiber.Ctx) error {
		t := c.Cookies("admin_ui")
		adminUISessionMu.Lock()
		exp, ok := adminUISessions[t]
		if !ok || now().After(exp) {
			adminUISessionMu.Unlock()
			return c.Redirect("/admin-ui/_login", fiber.StatusFound)
		}
		adminUISessions[t] = now().Add(adminUISessionTTL)
		adminUISessionMu.Unlock()
		return c.Next()
	}

	ui.Get("/", auth, func(c *fiber.Ctx) error {
		return c.Type("html", "utf-8").SendString(adminHTML(c))
	})

	ui.Post("/action/refresh_panel_stats", auth, func(c *fiber.Ctx) error {
		go refreshPanelStatsFromPython()
		return c.SendString("Опрос панелей запущен. Обновите страницу через несколько секунд.")
	})

	ui.Get("/export/payments.csv", auth, func(c *fiber.Ctx) error {
		rows, err := db.Query(`
        SELECT
            p.id,
            COALESCE(u.email, ''),
            p.provider,
            p.provider_payment_id,
            p.amount,
            p.currency,
            p.status,
            p.created_at,
            COALESCE(p.paid_at, '')
        FROM payments p
        LEFT JOIN users u ON u.id = p.user_id
        ORDER BY p.created_at DESC
    `)
		if err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		defer rows.Close()

		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=\"payments.csv\"")

		w := csv.NewWriter(c.Response().BodyWriter())
		_ = w.Write([]string{"id", "email", "provider", "provider_payment_id", "amount", "currency", "status", "created_at", "paid_at"})
		for rows.Next() {
			var (
				id         int64
				email      string
				provider   string
				providerID string
				amount     float64
				currency   string
				status     string
				createdAt  string
				paidAt     string
			)
			if err := rows.Scan(&id, &email, &provider, &providerID, &amount, &currency, &status, &createdAt, &paidAt); err != nil {
				continue
			}
			_ = w.Write([]string{
				fmt.Sprintf("%d", id),
				email,
				provider,
				providerID,
				fmt.Sprintf("%.2f", amount),
				currency,
				status,
				createdAt,
				paidAt,
			})
		}
		w.Flush()
		return nil
	})

	ui.Get("/export/users.csv", auth, func(c *fiber.Ctx) error {
		rows, err := db.Query(`
        SELECT
            u.id,
            u.email,
            u.balance,
            u.created_at,
            COALESCE(u.last_login, ''),
            u.blocked,
            COALESCE((SELECT ip FROM sessions s WHERE s.user_id = u.id ORDER BY s.created_at DESC LIMIT 1), '')
        FROM users u
        ORDER BY u.id DESC
    `)
		if err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		defer rows.Close()

		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=\"users.csv\"")

		w := csv.NewWriter(c.Response().BodyWriter())
		_ = w.Write([]string{"id", "email", "balance", "created_at", "last_login", "blocked", "last_ip"})
		for rows.Next() {
			var (
				id        int64
				email     string
				balance   float64
				createdAt string
				lastLogin string
				blocked   int
				lastIP    string
			)
			if err := rows.Scan(&id, &email, &balance, &createdAt, &lastLogin, &blocked, &lastIP); err != nil {
				continue
			}
			_ = w.Write([]string{
				fmt.Sprintf("%d", id),
				email,
				fmt.Sprintf("%.2f", balance),
				createdAt,
				lastLogin,
				fmt.Sprintf("%d", blocked),
				lastIP,
			})
		}
		w.Flush()
		return nil
	})

	ui.Post("/action/set_monthly", auth, func(c *fiber.Ctx) error {
		priceStr := strings.TrimSpace(c.FormValue("monthly"))
		if priceStr == "" {
			return c.Status(400).SendString("Введите цену")
		}
		p, err := strconv.ParseFloat(priceStr, 64)
		if err != nil || p <= 0 {
			return c.Status(400).SendString("Неверная цена")
		}
		if err := setGlobalMonthlyPrice(p); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Цена сохранена")
	})

	ui.Post("/action/promo_batch", auth, func(c *fiber.Ctx) error {
		nStr := strings.TrimSpace(c.FormValue("n"))
		amountStr := strings.TrimSpace(c.FormValue("amount"))
		usesStr := strings.TrimSpace(c.FormValue("uses"))
		expStr := strings.TrimSpace(c.FormValue("expires"))

		n, err := strconv.Atoi(nStr)
		if err != nil || n <= 0 || n > 10000 {
			return c.Status(400).SendString("n 1..10000")
		}
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			return c.Status(400).SendString("amount > 0")
		}
		uses, err := strconv.Atoi(usesStr)
		if err != nil || uses <= 0 {
			return c.Status(400).SendString("uses > 0")
		}

		var exp *string
		if expStr != "" {
			if _, e := time.Parse(time.RFC3339, expStr); e != nil {
				return c.Status(400).SendString("expires RFC3339 или пусто")
			}
			exp = &expStr
		}
		adminEmail := "admin-ui"
		var b strings.Builder
		fmt.Fprintf(&b, "%d промокодов на %.2f рублей с %d использ.\n", n, amount, uses)
		if exp != nil {
			fmt.Fprintf(&b, "Срок: %s\n", *exp)
		}
		fmt.Fprintln(&b, "--------------------------------")

		tx, _ := db.Begin()
		defer tx.Rollback()
		nowS := nowStr()
		for i := 0; i < n; i++ {
			code := genPromoCode()
			if _, err := tx.Exec("INSERT INTO promo_codes(code,amount,max_uses,expires_at,created_by,created_at) VALUES(?,?,?,?,?,?)",
				code, amount, uses, nullableStr(exp), adminEmail, nowS); err != nil {
				return c.Status(500).SendString("Ошибка вставки промокодов")
			}
			fmt.Fprintln(&b, code)
		}
		if err := tx.Commit(); err != nil {
			return c.Status(500).SendString("Ошибка коммита")
		}

		c.Set("Content-Type", "text/plain; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=\"promocodes.txt\"")
		return c.SendString(b.String())
	})

	ui.Post("/action/add_panel", auth, func(c *fiber.Ctx) error {
		name := strings.TrimSpace(c.FormValue("name"))
		country := strings.TrimSpace(c.FormValue("country"))
		baseURL := strings.TrimSpace(c.FormValue("base_url"))
		login := strings.TrimSpace(c.FormValue("login"))
		password := strings.TrimSpace(c.FormValue("password"))
		enabledStr := strings.TrimSpace(c.FormValue("enabled"))

		priorityStr := strings.TrimSpace(c.FormValue("priority"))
		region := strings.TrimSpace(c.FormValue("region"))
		role := strings.TrimSpace(c.FormValue("role"))

		vlessServer := strings.TrimSpace(c.FormValue("vless_server"))
		vlessPublicKey := strings.TrimSpace(c.FormValue("vless_public_key"))
		vlessShortID := strings.TrimSpace(c.FormValue("vless_short_id"))

		if name == "" || country == "" || baseURL == "" || login == "" || password == "" {
			return c.Status(400).SendString("Заполните все обязательные поля")
		}
		enabled := 1
		if enabledStr == "0" {
			enabled = 0
		}
		if region == "" {
			region = "EU"
		}
		if role == "" {
			role = "general"
		}
		priority := 100
		if priorityStr != "" {
			if p, err := strconv.Atoi(priorityStr); err == nil {
				priority = p
			}
		}

		_, err := db.Exec(`
            INSERT INTO vpn_panels(
                name,
                country,
                base_url,
                login,
                password,
                enabled,
                priority,
                region,
                role,
                vless_server,
                vless_public_key,
                vless_short_id,
                created_at
            )
            VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
        `, name, country, baseURL, login, password, enabled, priority, region, role,
			vlessServer, vlessPublicKey, vlessShortID, nowStr())
		if err != nil {
			log.Println("add_panel error:", err)
			return c.Status(500).SendString("Ошибка БД при сохранении панели")
		}
		return c.SendString("Панель сохранена")
	})

	ui.Post("/action/update_panel", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("panel_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID панели")
		}
		panelID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || panelID <= 0 {
			return c.Status(400).SendString("Некорректный ID панели")
		}

		type field struct {
			col string
			val any
		}
		var fields []field
		addField := func(col string, v string) {
			v = strings.TrimSpace(v)
			if v != "" {
				fields = append(fields, field{col: col, val: v})
			}
		}
		addField("name", c.FormValue("name"))
		addField("country", c.FormValue("country"))
		addField("base_url", c.FormValue("base_url"))
		addField("login", c.FormValue("login"))
		addField("password", c.FormValue("password"))
		addField("region", c.FormValue("region"))
		addField("role", c.FormValue("role"))
		addField("vless_server", c.FormValue("vless_server"))
		addField("vless_public_key", c.FormValue("vless_public_key"))
		addField("vless_short_id", c.FormValue("vless_short_id"))

		if v := strings.TrimSpace(c.FormValue("enabled")); v != "" {
			if v != "0" && v != "1" {
				return c.Status(400).SendString("enabled: 0 или 1")
			}
			fields = append(fields, field{col: "enabled", val: v})
		}
		if v := strings.TrimSpace(c.FormValue("priority")); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				fields = append(fields, field{col: "priority", val: p})
			} else {
				return c.Status(400).SendString("priority должно быть числом")
			}
		}

		if len(fields) == 0 {
			return c.Status(400).SendString("Нет полей для обновления")
		}

		var setParts []string
		var args []any
		for _, f := range fields {
			setParts = append(setParts, f.col+"=?")
			args = append(args, f.val)
		}
		setParts = append(setParts, "updated_at=?")
		args = append(args, nowStr(), panelID)

		q := "UPDATE vpn_panels SET " + strings.Join(setParts, ", ") + " WHERE id=?"
		if _, err := db.Exec(q, args...); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Панель обновлена")
	})

	ui.Post("/action/delete_panel", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("panel_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID панели")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			return c.Status(400).SendString("Некорректный ID панели")
		}
		if _, err := db.Exec(`DELETE FROM vpn_panels WHERE id=?`, id); err != nil {
			log.Println("delete_panel error:", err)
			return c.Status(500).SendString("Ошибка БД при удалении панели")
		}
		return c.SendString("Панель удалена")
	})

	ui.Post("/action/premium_create", auth, func(c *fiber.Ctx) error {
		panelIDStr := strings.TrimSpace(c.FormValue("panel_id"))
		usersStr := strings.TrimSpace(c.FormValue("users"))
		daysStr := strings.TrimSpace(c.FormValue("days"))

		panelID, err := strconv.ParseInt(panelIDStr, 10, 64)
		if err != nil || panelID <= 0 {
			return c.Status(400).SendString("Некорректный ID панели")
		}
		users, err := strconv.Atoi(usersStr)
		if err != nil || users <= 0 || users > 1000 {
			return c.Status(400).SendString("users 1..1000")
		}
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 || days > 3650 {
			return c.Status(400).SendString("days 1..3650")
		}

		premiumUntil := now().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		code := genPromoCode()

		tx, _ := db.Begin()
		defer tx.Rollback()

		if _, err := tx.Exec(
			`INSERT INTO premium_codes(code,panel_id,max_users,expires_at,premium_until,created_by,created_at)
             VALUES(?,?,?,?,?,?,?)`,
			code, panelID, users, premiumUntil, premiumUntil, "admin-ui", nowStr(),
		); err != nil {
			return c.Status(500).SendString("Ошибка создания премиум-кода")
		}
		if _, err := tx.Exec(
			`UPDATE vpn_panels SET premium_override=1, premium_until=? WHERE id=?`,
			premiumUntil, panelID,
		); err != nil {
			return c.Status(500).SendString("Ошибка обновления панели")
		}
		if err := tx.Commit(); err != nil {
			return c.Status(500).SendString("Ошибка коммита")
		}

		return c.SendString("Премиум-код: " + code + "\nДо: " + premiumUntil + "\nЛимит пользователей: " + fmt.Sprintf("%d", users))
	})

	ui.Post("/action/premium_extend", auth, func(c *fiber.Ctx) error {
		code := strings.ToUpper(strings.TrimSpace(c.FormValue("code")))
		addDaysStr := strings.TrimSpace(c.FormValue("add_days"))
		addUsersStr := strings.TrimSpace(c.FormValue("add_users"))

		if code == "" {
			return c.Status(400).SendString("Введите промокод")
		}
		addDays := 0
		if addDaysStr != "" {
			if v, err := strconv.Atoi(addDaysStr); err == nil {
				addDays = v
			} else {
				return c.Status(400).SendString("add_days должно быть числом")
			}
		}
		addUsers := 0
		if addUsersStr != "" {
			if v, err := strconv.Atoi(addUsersStr); err == nil {
				addUsers = v
			} else {
				return c.Status(400).SendString("add_users должно быть числом")
			}
		}
		if addDays == 0 && addUsers == 0 {
			return c.Status(400).SendString("Укажите add_days или add_users")
		}

		tx, _ := db.Begin()
		defer tx.Rollback()

		var (
			panelID      int64
			maxUsers     int
			premiumUntil string
		)
		if err := tx.QueryRow(
			`SELECT panel_id, max_users, premium_until FROM premium_codes WHERE code=?`,
			code,
		).Scan(&panelID, &maxUsers, &premiumUntil); err != nil {
			return c.Status(400).SendString("Код не найден")
		}

		newMaxUsers := maxUsers + addUsers
		if newMaxUsers < 1 {
			return c.Status(400).SendString("Недопустимый лимит пользователей")
		}

		newPremiumUntil := premiumUntil
		if addDays > 0 {
			if t, err := time.Parse(time.RFC3339, premiumUntil); err == nil {
				newPremiumUntil = t.Add(time.Duration(addDays) * 24 * time.Hour).Format(time.RFC3339)
			} else {
				return c.Status(500).SendString("Некорректная дата premium_until")
			}
		}

		if _, err := tx.Exec(
			`UPDATE premium_codes SET max_users=?, premium_until=?, expires_at=? WHERE code=?`,
			newMaxUsers, newPremiumUntil, newPremiumUntil, code,
		); err != nil {
			return c.Status(500).SendString("Ошибка обновления премиум-кода")
		}
		if _, err := tx.Exec(
			`UPDATE vpn_panels SET premium_override=1, premium_until=? WHERE id=?`,
			newPremiumUntil, panelID,
		); err != nil {
			return c.Status(500).SendString("Ошибка обновления панели")
		}
		if err := tx.Commit(); err != nil {
			return c.Status(500).SendString("Ошибка коммита")
		}

		return c.SendString("Готово. До: " + newPremiumUntil + "\nЛимит пользователей: " + fmt.Sprintf("%d", newMaxUsers))
	})

	ui.Post("/action/migrate_panel", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("panel_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID панели")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return c.Status(400).SendString("Некорректный ID панели")
		}
		if err := syncAllUsersToPanel(id); err != nil {
			log.Println("syncAllUsersToPanel error:", err)
			return c.Status(500).SendString("Ошибка запуска миграции")
		}
		return c.SendString("Миграция пользователей запущена")
	})

	ui.Post("/action/block_user", auth, func(c *fiber.Ctx) error {
		email := normEmail(c.FormValue("email"))
		if email == "" {
			return c.Status(400).SendString("Введите e-mail")
		}
		if _, err := db.Exec("UPDATE users SET blocked=1 WHERE email=?", email); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		db.Exec("UPDATE sessions SET revoked_at=? WHERE user_id IN (SELECT id FROM users WHERE email=?)",
			nowStr(), email)
		return c.SendString("Пользователь заблокирован")
	})

	ui.Post("/action/unblock_user", auth, func(c *fiber.Ctx) error {
		email := normEmail(c.FormValue("email"))
		if email == "" {
			return c.Status(400).SendString("Введите e-mail")
		}
		if _, err := db.Exec("UPDATE users SET blocked=0 WHERE email=?", email); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Пользователь разблокирован")
	})

	ui.Post("/action/premium_clear_panel", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("panel_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID панели")
		}
		panelID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || panelID <= 0 {
			return c.Status(400).SendString("Некорректный ID панели")
		}
		tx, _ := db.Begin()
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE vpn_panels SET premium_override=0, premium_until=NULL WHERE id=?`, panelID); err != nil {
			return c.Status(500).SendString("Ошибка обновления панели")
		}
		if _, err := tx.Exec(`DELETE FROM premium_memberships WHERE code IN (SELECT code FROM premium_codes WHERE panel_id=?)`, panelID); err != nil {
			return c.Status(500).SendString("Ошибка удаления премиум-подписок")
		}
		if _, err := tx.Exec(`DELETE FROM premium_codes WHERE panel_id=?`, panelID); err != nil {
			return c.Status(500).SendString("Ошибка удаления премиум-кодов")
		}
		if err := tx.Commit(); err != nil {
			return c.Status(500).SendString("Ошибка коммита")
		}
		return c.SendString("Премиум с панели снят")
	})

	ui.Post("/action/toggle_panel", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("panel_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID панели")
		}
		panelID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || panelID <= 0 {
			return c.Status(400).SendString("Некорректный ID панели")
		}
		var enabled int
		if err := db.QueryRow("SELECT enabled FROM vpn_panels WHERE id=?", panelID).Scan(&enabled); err != nil {
			return c.Status(400).SendString("Панель не найдена")
		}
		newVal := 1
		if enabled == 1 {
			newVal = 0
		}
		if _, err := db.Exec("UPDATE vpn_panels SET enabled=? WHERE id=?", newVal, panelID); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Готово")
	})

	ui.Post("/action/announcement_create", auth, func(c *fiber.Ctx) error {
		title := strings.TrimSpace(c.FormValue("title"))
		body := strings.TrimSpace(c.FormValue("body"))
		imageURL := strings.TrimSpace(c.FormValue("image_url"))
		ctaURL := strings.TrimSpace(c.FormValue("cta_url"))
		activeDaysStr := strings.TrimSpace(c.FormValue("active_days"))
		if title == "" || body == "" {
			return c.Status(400).SendString("Заполните заголовок и текст")
		}
		if len(title) > 120 || len(body) > 2000 {
			return c.Status(400).SendString("Слишком длинный заголовок или текст")
		}
		if len(imageURL) > 512 || len(ctaURL) > 512 {
			return c.Status(400).SendString("Слишком длинный URL")
		}
		var activeUntil any = nil
		if activeDaysStr != "" {
			days, err := strconv.Atoi(activeDaysStr)
			if err != nil || days <= 0 {
				return c.Status(400).SendString("active_days должно быть числом > 0")
			}
			activeUntil = now().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		}
		var imageVal any = nil
		var ctaVal any = nil
		if imageURL != "" {
			imageVal = imageURL
		}
		if ctaURL != "" {
			ctaVal = ctaURL
		}
		if _, err := db.Exec(
			`INSERT INTO announcements(title,body,image_url,cta_url,active_until,created_at) VALUES(?,?,?,?,?,?)`,
			title, body, imageVal, ctaVal, activeUntil, nowStr(),
		); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Объявление отправлено")
	})

	ui.Post("/action/announcement_delete", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("announcement_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID объявления")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			return c.Status(400).SendString("Некорректный ID")
		}
		if _, err := db.Exec(`DELETE FROM announcements WHERE id=?`, id); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Объявление удалено")
	})

	ui.Post("/action/version_create", auth, func(c *fiber.Ctx) error {
		platform := strings.TrimSpace(strings.ToLower(c.FormValue("platform")))
		vcStr := strings.TrimSpace(c.FormValue("version_code"))
		vn := strings.TrimSpace(c.FormValue("version_name"))
		url := strings.TrimSpace(c.FormValue("url"))
		msg := strings.TrimSpace(c.FormValue("message"))
		minReqStr := strings.TrimSpace(c.FormValue("min_required"))
		if platform == "" || vcStr == "" {
			return c.Status(400).SendString("platform и version_code обязательны")
		}
		vc, err := strconv.Atoi(vcStr)
		if err != nil {
			return c.Status(400).SendString("version_code должно быть числом")
		}
		minReq := 0
		if minReqStr == "1" {
			minReq = 1
		}
		if _, err := db.Exec(
			`INSERT INTO app_versions(platform,version_code,version_name,min_required,message,url,created_at) VALUES(?,?,?,?,?,?,?)`,
			platform, vc, vn, minReq, msg, url, nowStr(),
		); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Версия сохранена")
	})

	ui.Post("/action/version_delete", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("version_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID версии")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			return c.Status(400).SendString("Некорректный ID")
		}
		if _, err := db.Exec(`DELETE FROM app_versions WHERE id=?`, id); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Версия удалена")
	})

	ui.Post("/action/yookassa_update", auth, func(c *fiber.Ctx) error {
		enabled := strings.TrimSpace(c.FormValue("enabled"))
		shopID := strings.TrimSpace(c.FormValue("shop_id"))
		apiKey := strings.TrimSpace(c.FormValue("api_key"))
		returnURL := strings.TrimSpace(c.FormValue("return_url"))
		webhookUser := strings.TrimSpace(c.FormValue("webhook_user"))
		webhookPass := strings.TrimSpace(c.FormValue("webhook_pass"))

		if enabled == "1" {
			_ = setSetting("yookassa_enabled", "1")
		} else {
			_ = setSetting("yookassa_enabled", "0")
		}
		if shopID != "" {
			_ = setSetting("yookassa_shop_id", shopID)
		}
		if apiKey != "" {
			_ = setSetting("yookassa_api_key", apiKey)
		}
		if returnURL != "" {
			_ = setSetting("yookassa_return_url", returnURL)
		}
		if webhookUser != "" {
			_ = setSetting("yookassa_webhook_user", webhookUser)
		}
		if webhookPass != "" {
			_ = setSetting("yookassa_webhook_pass", webhookPass)
		}

		loadYooConfig()
		return c.SendString("Настройки YooKassa сохранены")
	})

	ui.Post("/action/ip_ban_add", auth, func(c *fiber.Ctx) error {
		ip := strings.TrimSpace(c.FormValue("ip"))
		reason := strings.TrimSpace(c.FormValue("reason"))
		expDaysStr := strings.TrimSpace(c.FormValue("expires_days"))
		if ip == "" {
			return c.Status(400).SendString("Введите IP")
		}
		var exp any = nil
		if expDaysStr != "" {
			days, err := strconv.Atoi(expDaysStr)
			if err != nil || days <= 0 {
				return c.Status(400).SendString("expires_days должно быть числом > 0")
			}
			exp = now().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		}
		if _, err := db.Exec(
			`INSERT INTO ip_bans(ip,reason,created_at,expires_at) VALUES(?,?,?,?)`,
			ip, nullableStr(&reason), nowStr(), exp,
		); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("IP заблокирован")
	})

	ui.Post("/action/ip_ban_remove", auth, func(c *fiber.Ctx) error {
		idStr := strings.TrimSpace(c.FormValue("ban_id"))
		if idStr == "" {
			return c.Status(400).SendString("Укажите ID бана")
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			return c.Status(400).SendString("Некорректный ID")
		}
		if _, err := db.Exec(`DELETE FROM ip_bans WHERE id=?`, id); err != nil {
			return c.Status(500).SendString("Ошибка БД")
		}
		return c.SendString("Бан снят")
	})
}

// ======================= EMAIL SENDER ==================
func sendEmail(to, subject, html string) error {
	if smtpHost == "" {
		log.Printf("[DEV MAIL] to=%s subj=%s\n%s\n", to, subject, html)
		return nil
	}
	fromName := "OffLag"
	fromAddr := smtpUser
	headers := map[string]string{
		"From":         fmt.Sprintf("%s <%s>", fromName, fromAddr),
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/html; charset=UTF-8",
		"Reply-To":     "support@offlag.app",
	}
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(html)

	addr := smtpHost + ":" + smtpPort
	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	return smtp.SendMail(addr, auth, fromAddr, []string{to}, []byte(b.String()))
}

// ---- HTML ----
func loginHTML(msg string) string {
	if msg != "" {
		msg = `<div style="color:#e11; margin-bottom:8px;">` + msg + `</div>`
	}
	return `<!doctype html><meta charset="utf-8">
<title>Admin Login</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
body{font-family:ui-sans-serif,system-ui,Segoe UI,Roboto,Arial,sans-serif;background:#0b0c12;color:#fff;display:grid;place-items:center;height:100dvh;margin:0}
.card{width:min(520px,92vw);background:#121521;border:1px solid #232638;border-radius:16px;padding:28px;box-shadow:0 10px 30px rgba(0,0,0,.35)}
h1{margin:0 0 16px;font-size:22px}
input[type=password]{width:100%;padding:12px 14px;border-radius:12px;border:1px solid #232638;background:#0c0f16;color:#fff;outline:none}
button{margin-top:12px;width:100%;padding:12px;border:0;border-radius:12px;background:#22d3ee;color:#001018;font-weight:700;cursor:pointer}
small{opacity:.7}
</style>
<div class="card">
  <h1>OffLag — Admin</h1>` + msg + `
  <form method="post" action="/admin-ui/_login">
    <input type="password" name="password" placeholder="Пароль из .env" autofocus autocomplete="current-password">
    <button type="submit">Войти</button>
  </form>
  <small>Установите <code>ADMIN_UI_PASSWORD</code> в .env</small>
</div>`
}

func adminHTML(c *fiber.Ctx) string {
	mp := getGlobalMonthlyPrice()
	payEmail := strings.TrimSpace(c.Query("pay_email"))
	payStatus := strings.TrimSpace(c.Query("pay_status"))
	payFrom := strings.TrimSpace(c.Query("pay_from"))
	payTo := strings.TrimSpace(c.Query("pay_to"))
	userEmail := strings.TrimSpace(c.Query("user_email"))
	statsDays := 90
	if v := strings.TrimSpace(c.Query("stats_days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 7 {
				n = 7
			}
			if n > 365 {
				n = 365
			}
			statsDays = n
		}
	}

	type panelRow struct {
		ID           int64
		Name         string
		Country      string
		Region       string
		Role         string
		BaseURL      string
		Enabled      bool
		Premium      bool
		PremiumUntil string
		Online       int
		Total        int
		UpdatedAt    string
	}
	var panels []panelRow

	type announcementRow struct {
		ID        int64
		Title     string
		CreatedAt string
		ActiveTil string
	}
	var annRows []announcementRow

	type versionRow struct {
		ID          int64
		Platform    string
		VersionCode int
		VersionName string
		MinRequired int
		CreatedAt   string
		URL         string
	}
	var verRows []versionRow

	type banRow struct {
		ID        int64
		IP        string
		Reason    string
		CreatedAt string
		ExpiresAt string
	}
	var banRows []banRow

	type paymentRow struct {
		ID         int64
		Email      string
		Provider   string
		ProviderID string
		Amount     float64
		Currency   string
		Status     string
		CreatedAt  string
		PaidAt     string
	}
	var payRows []paymentRow

	type userRow struct {
		ID        int64
		Email     string
		Balance   float64
		CreatedAt string
		LastLogin string
		Blocked   bool
		LastIP    string
	}
	var userRows []userRow

	type dayStat struct {
		Date     string
		NewUsers int
		Sessions int
		Payments int
		Revenue  float64
	}
	var statRows []dayStat
	type countryStatRow struct {
		Country string
		Online  int
		Total   int
	}
	var countryRows []countryStatRow
	type panelStatRow struct {
		PanelID int64
		Name    string
		Country string
		Online  int
		Total   int
	}
	var panelRows []panelStatRow

	rows, err := db.Query(`
        SELECT
            p.id,
            p.name,
            p.country,
            p.region,
            p.role,
            p.base_url,
            p.enabled,
            p.premium_override,
            COALESCE(p.premium_until, ''),
            COALESCE(s.online_users, 0) AS online_users,
            COALESCE(s.total_users, 0)  AS total_users,
            COALESCE(s.updated_at, '')  AS updated_at
        FROM vpn_panels p
        LEFT JOIN vpn_panel_stats s ON s.panel_id = p.id
        ORDER BY p.priority ASC, p.id ASC
    `)
	if err != nil {
		log.Println("adminHTML vpn_panels query error:", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var pr panelRow
			var enabledInt int
			var premiumInt int
			if err := rows.Scan(
				&pr.ID,
				&pr.Name,
				&pr.Country,
				&pr.Region,
				&pr.Role,
				&pr.BaseURL,
				&enabledInt,
				&premiumInt,
				&pr.PremiumUntil,
				&pr.Online,
				&pr.Total,
				&pr.UpdatedAt,
			); err == nil {
				pr.Enabled = enabledInt == 1
				pr.Premium = premiumInt == 1
				panels = append(panels, pr)
			} else {
				log.Println("adminHTML scan error:", err)
			}
		}
	}

	ann, err := db.Query(`
        SELECT id, title, created_at, COALESCE(active_until, '')
        FROM announcements
        ORDER BY created_at DESC
        LIMIT 20
    `)
	if err != nil {
		log.Println("adminHTML announcements query error:", err)
	} else {
		defer ann.Close()
		for ann.Next() {
			var r announcementRow
			if err := ann.Scan(&r.ID, &r.Title, &r.CreatedAt, &r.ActiveTil); err == nil {
				annRows = append(annRows, r)
			}
		}
	}

	ver, err := db.Query(`
        SELECT id, platform, version_code, COALESCE(version_name,''), min_required, created_at, COALESCE(url,'')
        FROM app_versions
        ORDER BY created_at DESC
        LIMIT 20
    `)
	if err != nil {
		log.Println("adminHTML app_versions query error:", err)
	} else {
		defer ver.Close()
		for ver.Next() {
			var r versionRow
			if err := ver.Scan(&r.ID, &r.Platform, &r.VersionCode, &r.VersionName, &r.MinRequired, &r.CreatedAt, &r.URL); err == nil {
				verRows = append(verRows, r)
			}
		}
	}

	bans, err := db.Query(`
        SELECT id, ip, COALESCE(reason,''), created_at, COALESCE(expires_at,'')
        FROM ip_bans
        ORDER BY created_at DESC
        LIMIT 50
    `)
	if err != nil {
		log.Println("adminHTML ip_bans query error:", err)
	} else {
		defer bans.Close()
		for bans.Next() {
			var r banRow
			if err := bans.Scan(&r.ID, &r.IP, &r.Reason, &r.CreatedAt, &r.ExpiresAt); err == nil {
				banRows = append(banRows, r)
			}
		}
	}

	// payments with filters
	payWhere := []string{"1=1"}
	var payArgs []any
	if payEmail != "" {
		payWhere = append(payWhere, "u.email LIKE ?")
		payArgs = append(payArgs, "%"+payEmail+"%")
	}
	if payStatus != "" {
		payWhere = append(payWhere, "p.status = ?")
		payArgs = append(payArgs, payStatus)
	}
	if payFrom != "" {
		payWhere = append(payWhere, "date(p.created_at) >= date(?)")
		payArgs = append(payArgs, payFrom)
	}
	if payTo != "" {
		payWhere = append(payWhere, "date(p.created_at) <= date(?)")
		payArgs = append(payArgs, payTo)
	}
	payQuery := `
        SELECT
            p.id,
            COALESCE(u.email,''),
            p.provider,
            p.provider_payment_id,
            p.amount,
            p.currency,
            p.status,
            p.created_at,
            COALESCE(p.paid_at,'')
        FROM payments p
        LEFT JOIN users u ON u.id = p.user_id
        WHERE ` + strings.Join(payWhere, " AND ") + `
        ORDER BY p.created_at DESC
        LIMIT 500
    `
	pays, err := db.Query(payQuery, payArgs...)
	if err != nil {
		log.Println("adminHTML payments query error:", err)
	} else {
		defer pays.Close()
		for pays.Next() {
			var r paymentRow
			if err := pays.Scan(&r.ID, &r.Email, &r.Provider, &r.ProviderID, &r.Amount, &r.Currency, &r.Status, &r.CreatedAt, &r.PaidAt); err == nil {
				payRows = append(payRows, r)
			}
		}
	}

	// users with filters
	userWhere := []string{"1=1"}
	var userArgs []any
	if userEmail != "" {
		userWhere = append(userWhere, "u.email LIKE ?")
		userArgs = append(userArgs, "%"+userEmail+"%")
	}
	userQuery := `
        SELECT
            u.id,
            u.email,
            u.balance,
            u.created_at,
            COALESCE(u.last_login,''),
            u.blocked,
            COALESCE((SELECT ip FROM sessions s WHERE s.user_id = u.id ORDER BY s.created_at DESC LIMIT 1), '')
        FROM users u
        WHERE ` + strings.Join(userWhere, " AND ") + `
        ORDER BY u.id DESC
        LIMIT 500
    `
	users, err := db.Query(userQuery, userArgs...)
	if err != nil {
		log.Println("adminHTML users query error:", err)
	} else {
		defer users.Close()
		for users.Next() {
			var r userRow
			var blockedInt int
			if err := users.Scan(&r.ID, &r.Email, &r.Balance, &r.CreatedAt, &r.LastLogin, &blockedInt, &r.LastIP); err == nil {
				r.Blocked = blockedInt == 1
				userRows = append(userRows, r)
			}
		}
	}

	statsMap := make(map[string]*dayStat)
	for i := 0; i < statsDays; i++ {
		d := now().AddDate(0, 0, -i).Format("2006-01-02")
		statsMap[d] = &dayStat{Date: d}
	}

	if rows, err := db.Query(`SELECT date(created_at), COUNT(*) FROM users GROUP BY date(created_at)`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			var c int
			if err := rows.Scan(&d, &c); err == nil {
				if st, ok := statsMap[d]; ok {
					st.NewUsers = c
				}
			}
		}
	}

	if rows, err := db.Query(`SELECT date(created_at), COUNT(*) FROM sessions GROUP BY date(created_at)`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			var c int
			if err := rows.Scan(&d, &c); err == nil {
				if st, ok := statsMap[d]; ok {
					st.Sessions = c
				}
			}
		}
	}

	if rows, err := db.Query(`SELECT date(created_at), COUNT(*), COALESCE(SUM(amount),0) FROM payments WHERE status='succeeded' GROUP BY date(created_at)`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			var c int
			var sum float64
			if err := rows.Scan(&d, &c, &sum); err == nil {
				if st, ok := statsMap[d]; ok {
					st.Payments = c
					st.Revenue = sum
				}
			}
		}
	}

	for i := 0; i < statsDays; i++ {
		d := now().AddDate(0, 0, -i).Format("2006-01-02")
		if st, ok := statsMap[d]; ok {
			statRows = append(statRows, *st)
		}
	}

	fromDay := now().AddDate(0, 0, -(statsDays - 1)).Format("2006-01-02")
	if rows, err := db.Query(`
        SELECT p.country, COALESCE(SUM(d.online_users),0), COALESCE(SUM(d.total_users),0)
        FROM vpn_panel_stats_daily d
        JOIN vpn_panels p ON p.id = d.panel_id
        WHERE d.day >= ?
        GROUP BY p.country
        ORDER BY p.country ASC
    `, fromDay); err == nil {
		defer rows.Close()
		for rows.Next() {
			var r countryStatRow
			if err := rows.Scan(&r.Country, &r.Online, &r.Total); err == nil {
				countryRows = append(countryRows, r)
			}
		}
	}

	if rows, err := db.Query(`
        SELECT p.id, p.name, p.country, COALESCE(SUM(d.online_users),0), COALESCE(SUM(d.total_users),0)
        FROM vpn_panel_stats_daily d
        JOIN vpn_panels p ON p.id = d.panel_id
        WHERE d.day >= ?
        GROUP BY p.id, p.name, p.country
        ORDER BY p.priority ASC, p.id ASC
    `, fromDay); err == nil {
		defer rows.Close()
		for rows.Next() {
			var r panelStatRow
			if err := rows.Scan(&r.PanelID, &r.Name, &r.Country, &r.Online, &r.Total); err == nil {
				panelRows = append(panelRows, r)
			}
		}
	}

	var b strings.Builder
	yooEnabledVal := "0"
	if getSetting("yookassa_enabled") == "1" || yooEnabled {
		yooEnabledVal = "1"
	}
	yooShop := strings.TrimSpace(getSetting("yookassa_shop_id"))
	yooKey := strings.TrimSpace(getSetting("yookassa_api_key"))
	yooReturn := strings.TrimSpace(getSetting("yookassa_return_url"))
	yooWhUser := strings.TrimSpace(getSetting("yookassa_webhook_user"))
	yooWhPass := strings.TrimSpace(getSetting("yookassa_webhook_pass"))

	maskedKey := ""
	if yooKey != "" {
		if len(yooKey) <= 4 {
			maskedKey = "****" + yooKey
		} else {
			maskedKey = "****" + yooKey[len(yooKey)-4:]
		}
	}
	maskedPass := ""
	if yooWhPass != "" {
		if len(yooWhPass) <= 4 {
			maskedPass = "****" + yooWhPass
		} else {
			maskedPass = "****" + yooWhPass[len(yooWhPass)-4:]
		}
	}

	b.WriteString(`<!doctype html><meta charset="utf-8">
<title>Admin UI</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
:root{--bg:#0b0c12;--card:#121521;--card2:#0f1118;--muted:#9aa3b2;--acc:#22d3ee;--bd:#232638;--good:#22c55e;--bad:#ef4444}
*{box-sizing:border-box}
body{background:var(--bg);color:#f8fafc;font-family:ui-sans-serif,system-ui,Segoe UI,Roboto,Arial,sans-serif;margin:0}
.app{display:grid;grid-template-columns:250px 1fr;min-height:100vh}
.sidebar{position:sticky;top:0;height:100vh;padding:20px;background:#0c0f16;border-right:1px solid var(--bd)}
.brand{font-size:18px;font-weight:800;letter-spacing:.2px}
.sub{color:var(--muted);font-size:12px;margin-top:4px}
.nav{margin-top:18px;display:grid;gap:6px}
.nav a{padding:10px 12px;border-radius:10px;color:var(--muted);text-decoration:none;border:1px solid transparent}
.nav a.active{color:#fff;background:#141827;border-color:var(--bd)}
.content{padding:24px}
.topbar{display:flex;align-items:center;justify-content:space-between;margin-bottom:18px}
h1{font-size:22px;margin:0}
h2{font-size:18px;margin:0}
.section{display:none}
.section.active{display:block}
.section-head{display:flex;align-items:center;justify-content:space-between;margin:6px 0 12px}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:16px}
.card{background:var(--card);border:1px solid var(--bd);border-radius:16px;padding:16px}
.card h3{margin:0 0 10px;font-size:16px}
.row{display:grid;gap:8px;margin-top:8px}
.row-2{display:grid;grid-template-columns:1fr 1fr;gap:10px}
input,select,textarea{padding:10px 12px;background:#0c0f16;border:1px solid var(--bd);border-radius:10px;color:#fff}
textarea{font-family:inherit;resize:vertical}
button,.btn{padding:10px 12px;border:0;border-radius:10px;background:var(--acc);color:#001018;font-weight:800;cursor:pointer;text-decoration:none;display:inline-block}
.btn-ghost{background:transparent;color:#fff;border:1px solid var(--bd)}
.btn-danger{background:transparent;color:#fff;border:1px solid #3b1d1d}
.btn-small{padding:6px 10px;font-size:12px}
small{color:var(--muted)}
hr{border:0;border-top:1px solid var(--bd);margin:12px 0}
label{font-size:12px;color:var(--muted)}
.table-wrap{overflow:auto;border:1px solid var(--bd);border-radius:12px;background:var(--card2)}
table{width:100%;min-width:980px;border-collapse:collapse;font-size:13px}
th,td{padding:8px 10px;border-bottom:1px solid var(--bd);text-align:left;vertical-align:middle}
th{font-weight:600;color:var(--muted)}
.badge{display:inline-block;padding:2px 8px;border-radius:999px;font-size:11px;border:1px solid var(--bd)}
.badge-on{border-color:var(--good);color:var(--good)}
.badge-off{border-color:var(--bad);color:var(--bad)}
.mono{font-family:ui-monospace,Consolas,monospace;font-size:12px}
.span-2{grid-column:1/-1}
.actions{display:flex;gap:8px;flex-wrap:wrap}
@media (max-width: 920px){
  .app{grid-template-columns:1fr}
  .sidebar{position:static;height:auto}
}
</style>
<div class="app">
  <aside class="sidebar">
    <div class="brand">OffLag Admin</div>
    <div class="sub">панель управления</div>
    <nav class="nav">
      <a href="#servers" data-section="servers" class="active">Серверы</a>
      <a href="#price" data-section="price">Цена и оплата</a>
      <a href="#users" data-section="users">Пользователи</a>
      <a href="#versions" data-section="versions">Версии и объявления</a>
      <a href="#stats" data-section="stats">Статистика</a>
    </nav>
  </aside>
  <main class="content">
    <div class="topbar">
      <h1>Серверы</h1>
    </div>
`)

	// ====== серверы ======
	b.WriteString(`
  <section id="servers" class="section active">
    <div class="section-head">
      <h2>Серверы</h2>
      <div class="actions">
        <form method="post" action="/admin-ui/action/refresh_panel_stats">
          <button class="btn-ghost btn-small" type="submit">Обновить онлайн</button>
        </form>
      </div>
    </div>
    <div class="grid">
      <div class="card span-2">
        <h3>Таблица серверов</h3>
`)
	if len(panels) == 0 {
		b.WriteString(`<p style="margin-top:10px;color:var(--muted);font-size:13px;">Серверы пока не добавлены.</p>`)
	} else {
		b.WriteString(`
        <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Панель</th>
              <th>Страна</th>
              <th>Регион / роль</th>
              <th>Онлайн / всего</th>
              <th>Премиум</th>
              <th>Статус</th>
              <th>Обновлено</th>
              <th>Действия</th>
            </tr>
          </thead>
          <tbody>`)

		for _, p := range panels {
			base := p.BaseURL
			if len(base) > 48 {
				base = base[:45] + "..."
			}
			updated := p.UpdatedAt
			if strings.TrimSpace(updated) == "" {
				updated = "?"
			}
			premiumText := "OFF"
			if p.Premium {
				if strings.TrimSpace(p.PremiumUntil) != "" {
					premiumText = "ON до " + p.PremiumUntil
				} else {
					premiumText = "ON"
				}
			}
			statusClass := "badge-off"
			statusText := "OFF"
			if p.Enabled {
				statusClass = "badge-on"
				statusText = "ON"
			}

			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", p.ID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(p.Name) + `<br><span class="mono">` + htmlEscape(base) + `</span></td>`)
			b.WriteString(`<td>` + htmlEscape(p.Country) + `</td>`)
			b.WriteString(`<td><span class="mono">` + htmlEscape(p.Region) + `</span><br><small>` + htmlEscape(p.Role) + `</small></td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d / %d", p.Online, p.Total) + `</td>`)
			b.WriteString(`<td><span class="mono">` + htmlEscape(premiumText) + `</span></td>`)
			b.WriteString(`<td><span class="badge ` + statusClass + `">` + statusText + `</span></td>`)
			b.WriteString(`<td><span class="mono">` + htmlEscape(updated) + `</span></td>`)
			b.WriteString(`<td>
<form method="post" action="/admin-ui/action/toggle_panel" style="display:inline">
  <input type="hidden" name="panel_id" value="` + fmt.Sprintf("%d", p.ID) + `">
  <button class="btn-ghost btn-small">` + func() string {
				if p.Enabled {
					return "Выключить"
				}
				return "Включить"
			}() + `</button>
</form>
<form method="post" action="/admin-ui/action/premium_clear_panel" style="display:inline;margin-left:6px" onsubmit="return confirm('Сбросить премиум у сервера ` + fmt.Sprintf("%d", p.ID) + `?')">
  <input type="hidden" name="panel_id" value="` + fmt.Sprintf("%d", p.ID) + `">
  <button class="btn-ghost btn-small">Сброс премиума</button>
</form>
<form method="post" action="/admin-ui/action/delete_panel" style="display:inline;margin-left:6px" onsubmit="return confirm('Удалить сервер ` + fmt.Sprintf("%d", p.ID) + `?')">
  <input type="hidden" name="panel_id" value="` + fmt.Sprintf("%d", p.ID) + `">
  <button class="btn-ghost btn-small">Удалить</button>
</form>
</td>`)
			b.WriteString(`</tr>`)
		}

		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</div>`)

	b.WriteString(`
      <div class="card">
        <h3>Добавить сервер</h3>
        <form method="post" action="/admin-ui/action/add_panel" class="row">
          <input name="name" placeholder="Название сервера (например NL #1)" required>
          <input name="country" placeholder="Страна (например NL, DE)" required>
          <input name="base_url" placeholder="Базовый URL панели (https://...)" required>
          <input name="login" placeholder="Логин admin панели" required>
          <input name="password" placeholder="Пароль admin панели" required>
          <input name="enabled" placeholder="1 — включена, 0 — выключена" value="1">
          <input name="priority" placeholder="Приоритет (меньше = важнее, по умолчанию 100)">
          <input name="region" placeholder="Регион (например EU, RU, ASIA)" value="EU">
          <input name="role" placeholder="Роль (general, gaming, streaming...)" value="general">
          <input name="vless_server" placeholder="VLESS server (IP/host)" required>
          <input name="vless_public_key" placeholder="Reality public_key" required>
          <input name="vless_short_id" placeholder="Reality short_id" required>
          <button>Сохранить сервер</button>
          <small>Данные используются для синхронизации 3x-ui и генерации конфигов.</small>
        </form>
      </div>

      <div class="card">
        <h3>Редактировать сервер</h3>
        <form method="post" action="/admin-ui/action/update_panel" class="row">
          <input name="panel_id" placeholder="ID сервера" required>
          <input name="name" placeholder="Название (опц.)">
          <input name="country" placeholder="Страна (опц.)">
          <input name="base_url" placeholder="Базовый URL (опц.)">
          <input name="login" placeholder="Логин (опц.)">
          <input name="password" placeholder="Пароль (опц.)">
          <input name="enabled" placeholder="enabled: 1/0 (опц.)">
          <input name="priority" placeholder="priority (опц.)">
          <input name="region" placeholder="Регион (опц.)">
          <input name="role" placeholder="Роль (опц.)">
          <input name="vless_server" placeholder="VLESS server (опц.)">
          <input name="vless_public_key" placeholder="Reality public_key (опц.)">
          <input name="vless_short_id" placeholder="Reality short_id (опц.)">
          <button>Обновить сервер</button>
          <small>Оставьте пустым то поле, которое не меняете.</small>
        </form>
      </div>

      <div class="card">
        <h3>Премиум: создать код</h3>
        <form method="post" action="/admin-ui/action/premium_create" class="row">
          <div class="row-2">
            <input name="panel_id" placeholder="ID сервера" required>
            <input name="users" placeholder="Пользователей (например 5)" required>
          </div>
          <input name="days" placeholder="Дней (например 30)" required>
          <button>Сгенерировать премиум-код</button>
        </form>
      </div>

      <div class="card">
        <h3>Премиум: продлить/расширить</h3>
        <form method="post" action="/admin-ui/action/premium_extend" class="row">
          <input name="code" placeholder="Код" required>
          <div class="row-2">
            <input name="add_days" placeholder="+ дней (можно 0)" value="0">
            <input name="add_users" placeholder="+ пользователей (можно 0)" value="0">
          </div>
          <button>Обновить</button>
        </form>
      </div>

      <div class="card">
        <h3>Опасные действия</h3>
        <form method="post" action="/admin-ui/action/premium_clear_panel" class="row">
          <label>Сбросить премиум у сервера</label>
          <input name="panel_id" placeholder="ID сервера" required>
          <button class="btn-ghost">Сброс премиума</button>
        </form>
        <hr>
        <form method="post" action="/admin-ui/action/delete_panel" class="row">
          <label>Удалить сервер</label>
          <input name="panel_id" placeholder="ID сервера" required>
          <button class="btn-danger">Удалить сервер</button>
        </form>
        <hr>
        <form method="post" action="/admin-ui/action/migrate_panel" class="row">
          <label>Миграция пользователей</label>
          <input name="panel_id" placeholder="ID сервера" required>
          <button class="btn-ghost">Запустить миграцию</button>
        </form>
      </div>
    </div>
  </section>
`)

	// ====== цена ======
	b.WriteString(`
  <section id="price" class="section">
    <div class="section-head">
      <h2>Цена и оплата</h2>
      <div class="actions">
        <a class="btn-ghost btn-small" href="/admin-ui/export/payments.csv">Экспорт платежей (CSV)</a>
      </div>
    </div>
    <div class="grid">
      <div class="card">
        <h3>Цена /мес (глобальная)</h3>
        <form method="post" action="/admin-ui/action/set_monthly" class="row">
          <label>Текущая: <b>` + fmt.Sprintf("%.2f ₽", mp) + `</b></label>
          <input name="monthly" placeholder="например 60.00" required>
          <button>Сохранить цену</button>
          <small>Суточная стоимость = цена / 30.</small>
        </form>
      </div>
      <div class="card">
        <h3>YooKassa</h3>
        <form method="post" action="/admin-ui/action/yookassa_update" class="row">
          <div>
            <label>Состояние</label>
            <div class="actions" style="margin-top:6px">
              <input type="hidden" name="enabled" value="` + func() string {
		if yooEnabledVal == "1" {
			return "0"
		}
		return "1"
	}() + `">
              <button class="` + func() string {
		if yooEnabledVal == "1" {
			return "btn-danger"
		}
		return "btn-ghost"
	}() + `" type="submit">` + func() string {
		if yooEnabledVal == "1" {
			return "Выключить оплату"
		}
		return "Включить оплату"
	}() + `</button>
              <span class="badge ` + func() string {
		if yooEnabledVal == "1" {
			return "badge-on"
		}
		return "badge-off"
	}() + `">` + func() string {
		if yooEnabledVal == "1" {
			return "ON"
		}
		return "OFF"
	}() + `</span>
            </div>
          </div>
          <input name="shop_id" placeholder="YOOKASSA_SHOP_ID" value="` + htmlEscape(yooShop) + `">
          <input name="api_key" placeholder="YOOKASSA_API_KEY" value="` + htmlEscape(yooKey) + `">
          <input name="return_url" placeholder="Return URL (https://...)" value="` + htmlEscape(yooReturn) + `">
          <input name="webhook_user" placeholder="Webhook Basic user" value="` + htmlEscape(yooWhUser) + `">
          <input name="webhook_pass" placeholder="Webhook Basic pass" value="` + htmlEscape(yooWhPass) + `">
          <button>Сохранить конфигурацию</button>
          <small>При изменении параметров потребуется перезапуск сервиса.</small>
        </form>
        <hr>
        <div class="mono" style="font-size:12px;line-height:1.5">
          <div><b>Текущая конфигурация</b></div>
          <div>enabled: ` + func() string {
		if yooEnabledVal == "1" {
			return "true"
		}
		return "false"
	}() + `</div>
          <div>shop_id: ` + htmlEscape(yooShop) + `</div>
          <div>api_key: ` + htmlEscape(maskedKey) + `</div>
          <div>return_url: ` + htmlEscape(yooReturn) + `</div>
          <div>webhook_user: ` + htmlEscape(yooWhUser) + `</div>
          <div>webhook_pass: ` + htmlEscape(maskedPass) + `</div>
        </div>
      </div>
      <div class="card span-2">
        <h3>Логи платежей</h3>
        <form method="get" action="/admin-ui" class="row">
          <input type="hidden" name="tab" value="price">
          <input name="pay_email" placeholder="Фильтр email">
          <input name="pay_status" placeholder="Статус (pending/succeeded/...)">
          <div class="row-2">
            <input type="date" name="pay_from" placeholder="с даты">
            <input type="date" name="pay_to" placeholder="по дату">
          </div>
          <button class="btn-ghost">Фильтровать</button>
        </form>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Email</th>
                <th>Провайдер</th>
                <th>Payment ID</th>
                <th>Сумма</th>
                <th>Статус</th>
                <th>Создан</th>
                <th>Оплачен</th>
              </tr>
            </thead>
            <tbody>
`)
	if len(payRows) == 0 {
		b.WriteString(`<tr><td colspan="8" style="color:var(--muted);">Нет платежей</td></tr>`)
	} else {
		for _, r := range payRows {
			paid := r.PaidAt
			if strings.TrimSpace(paid) == "" {
				paid = "?"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.ID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Email) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Provider) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.ProviderID) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%.2f %s", r.Amount, htmlEscape(r.Currency)) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Status) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.CreatedAt) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(paid) + `</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>
    </div>
  </section>
`)

	// ====== пользователи ======
	b.WriteString(`
  <section id="users" class="section">
    <div class="section-head">
      <h2>Пользователи</h2>
      <div class="actions">
        <a class="btn-ghost btn-small" href="/admin-ui/export/users.csv">Экспорт пользователей (CSV)</a>
      </div>
    </div>
    <div class="grid">
      <div class="card span-2">
        <h3>Фильтр пользователей</h3>
        <form method="get" action="/admin-ui" class="row">
          <input type="hidden" name="tab" value="users">
          <input name="user_email" placeholder="Фильтр email">
          <button class="btn-ghost">Фильтровать</button>
        </form>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Email</th>
                <th>Баланс</th>
                <th>Создан</th>
                <th>Последний вход</th>
                <th>IP</th>
                <th>Статус</th>
                <th>Действия</th>
              </tr>
            </thead>
            <tbody>
`)
	if len(userRows) == 0 {
		b.WriteString(`<tr><td colspan="8" style="color:var(--muted);">Нет пользователей</td></tr>`)
	} else {
		for _, u := range userRows {
			status := `<span class="badge badge-on">OK</span>`
			if u.Blocked {
				status = `<span class="badge badge-off">BLOCK</span>`
			}
			lastLogin := u.LastLogin
			if strings.TrimSpace(lastLogin) == "" {
				lastLogin = "?"
			}
			lastIP := u.LastIP
			if strings.TrimSpace(lastIP) == "" {
				lastIP = "?"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", u.ID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(u.Email) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%.2f ₽", u.Balance) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(u.CreatedAt) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(lastLogin) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(lastIP) + `</td>`)
			b.WriteString(`<td>` + status + `</td>`)
			b.WriteString(`<td>
<form method="post" action="/admin-ui/action/block_user" style="display:inline">
  <input type="hidden" name="email" value="` + htmlEscape(u.Email) + `">
  <button class="btn-ghost btn-small">Блок</button>
</form>
<form method="post" action="/admin-ui/action/unblock_user" style="display:inline;margin-left:6px">
  <input type="hidden" name="email" value="` + htmlEscape(u.Email) + `">
  <button class="btn-ghost btn-small">Разблок.</button>
</form>
`)
			if strings.TrimSpace(u.LastIP) != "" {
				b.WriteString(`<form method="post" action="/admin-ui/action/ip_ban_add" style="display:inline;margin-left:6px">
  <input type="hidden" name="ip" value="` + htmlEscape(u.LastIP) + `">
  <input type="hidden" name="reason" value="user ban">
  <button class="btn-danger btn-small">IP бан</button>
</form>`)
			}
			b.WriteString(`</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>`)

	b.WriteString(`
      <div class="card">
        <h3>Блокировка по email</h3>
        <form method="post" action="/admin-ui/action/block_user" class="row">
          <input name="email" placeholder="user@example.com" required>
          <button class="btn-danger">Заблокировать</button>
        </form>
        <hr>
        <form method="post" action="/admin-ui/action/unblock_user" class="row">
          <input name="email" placeholder="user@example.com" required>
          <button class="btn-ghost">Разблокировать</button>
        </form>
      </div>

      <div class="card span-2">
        <h3>IP-бан</h3>
        <form method="post" action="/admin-ui/action/ip_ban_add" class="row">
          <input name="ip" placeholder="IP (например 1.2.3.4)" required>
          <input name="reason" placeholder="Причина (опц.)">
          <input name="expires_days" placeholder="Дней (опц.)">
          <button class="btn-danger">Забанить IP</button>
        </form>
        <hr>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>IP</th>
                <th>Причина</th>
                <th>Создан</th>
                <th>Истекает</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
`)
	if len(banRows) == 0 {
		b.WriteString(`<tr><td colspan="6" style="color:var(--muted);">Нет банов</td></tr>`)
	} else {
		for _, r := range banRows {
			exp := r.ExpiresAt
			if strings.TrimSpace(exp) == "" {
				exp = "?"
			}
			reason := r.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "?"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.ID) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.IP) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(reason) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.CreatedAt) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(exp) + `</td>`)
			b.WriteString(`<td>
<form method="post" action="/admin-ui/action/ip_ban_remove" onsubmit="return confirm('Снять бан ` + fmt.Sprintf("%d", r.ID) + `?')">
  <input type="hidden" name="ban_id" value="` + fmt.Sprintf("%d", r.ID) + `">
  <button class="btn-ghost btn-small">Снять</button>
</form>
</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>
    </div>
  </section>
`)

	// ====== версии ======
	b.WriteString(`
  <section id="versions" class="section">
    <div class="section-head">
      <h2>Версии и объявления</h2>
    </div>
    <div class="grid">
      <div class="card">
        <h3>Версии приложения</h3>
        <form method="post" action="/admin-ui/action/version_create" class="row">
          <input name="platform" placeholder="platform (android/ios)" required>
          <input name="version_code" placeholder="version_code (int)" required>
          <input name="version_name" placeholder="version_name (например 1.2.3)">
          <input name="url" placeholder="URL обновления">
          <input name="message" placeholder="Сообщение обновления">
          <select name="min_required">
            <option value="0">Необязательное</option>
            <option value="1">Обязательное</option>
          </select>
          <button>Сохранить</button>
        </form>
        <hr>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Платформа</th>
                <th>Код</th>
                <th>Имя</th>
                <th>Мин.</th>
                <th>Создан</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
`)
	if len(verRows) == 0 {
		b.WriteString(`<tr><td colspan="7" style="color:var(--muted);">Нет версий</td></tr>`)
	} else {
		for _, r := range verRows {
			minReq := "нет"
			if r.MinRequired == 1 {
				minReq = "да"
			}
			name := r.VersionName
			if strings.TrimSpace(name) == "" {
				name = "?"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.ID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(strings.ToUpper(r.Platform)) + `</td>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.VersionCode) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(name) + `</td>`)
			b.WriteString(`<td>` + minReq + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.CreatedAt) + `</td>`)
			b.WriteString(`<td>
<form method="post" action="/admin-ui/action/version_delete" onsubmit="return confirm('Удалить версию '+` + fmt.Sprintf("%d", r.ID) + `+'?')">
  <input type="hidden" name="version_id" value="` + fmt.Sprintf("%d", r.ID) + `">
  <button class="btn-ghost btn-small">Удалить</button>
</form>
</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>
      </div>

      <div class="card">
        <h3>Объявления</h3>
        <form method="post" action="/admin-ui/action/announcement_create" class="row">
          <input name="title" placeholder="Заголовок" required>
          <textarea name="body" placeholder="Текст" rows="4"></textarea>
          <input name="image_url" placeholder="URL изображения (опц.)">
          <input name="cta_url" placeholder="URL кнопки (опц.)">
          <input name="active_days" placeholder="Дней (опц.)">
          <button>Сохранить</button>
          <small>Объявление видно один раз для каждого пользователя.</small>
        </form>
        <hr>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Заголовок</th>
                <th>Создан</th>
                <th>Активно до</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
`)
	if len(annRows) == 0 {
		b.WriteString(`<tr><td colspan="5" style="color:var(--muted);">Нет объявлений</td></tr>`)
	} else {
		for _, r := range annRows {
			active := r.ActiveTil
			if strings.TrimSpace(active) == "" {
				active = "?"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.ID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Title) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(r.CreatedAt) + `</td>`)
			b.WriteString(`<td class="mono">` + htmlEscape(active) + `</td>`)
			b.WriteString(`<td>
<form method="post" action="/admin-ui/action/announcement_delete" onsubmit="return confirm('Удалить объявление '+` + fmt.Sprintf("%d", r.ID) + `+'?')">
  <input type="hidden" name="announcement_id" value="` + fmt.Sprintf("%d", r.ID) + `">
  <button class="btn-ghost btn-small">Удалить</button>
</form>
</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>
      </div>

      <div class="card">
        <h3>Промокоды (пакетом, TXT)</h3>
        <form method="post" action="/admin-ui/action/promo_batch" class="row">
          <input name="n" placeholder="Сколько кодов (например 100)" required>
          <input name="amount" placeholder="Сумма (например 60.00)" required>
          <input name="uses" placeholder="Использований на код (например 1)" required>
          <input name="expires" placeholder="Срок (RFC3339) или пусто">
          <button>Сгенерировать и скачать .txt</button>
          <small>Файл содержит список кодов построчно.</small>
        </form>
      </div>
    </div>
  </section>
`)

	// ====== статистика ======
	b.WriteString(`
  <section id="stats" class="section">
    <div class="section-head">
      <h2>Статистика по дням</h2>
      <form method="get" action="/admin-ui" class="row">
        <input type="hidden" name="tab" value="stats">
        <input name="stats_days" type="number" min="7" max="365" placeholder="Дней (например 90)">
        <button class="btn-ghost">Показать</button>
      </form>
    </div>
    <div class="grid">
      <div class="card span-2">
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Дата</th>
                <th>Новые пользователи</th>
                <th>Сессии</th>
                <th>Платежи</th>
                <th>Выручка</th>
              </tr>
            </thead>
            <tbody>
`)
	if len(statRows) == 0 {
		b.WriteString(`<tr><td colspan="5" style="color:var(--muted);">Нет данных</td></tr>`)
	} else {
		for _, s := range statRows {
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + htmlEscape(s.Date) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", s.NewUsers) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", s.Sessions) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", s.Payments) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%.2f ₽", s.Revenue) + `</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>

      <div class="card">
        <h3>По странам (только за период)</h3>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Страна</th>
                <th>Онлайн (сумма)</th>
                <th>Всего (сумма)</th>
              </tr>
            </thead>
            <tbody>
`)
	if len(countryRows) == 0 {
		b.WriteString(`<tr><td colspan="3" style="color:var(--muted);">Нет данных</td></tr>`)
	} else {
		for _, r := range countryRows {
			b.WriteString(`<tr>`)
			b.WriteString(`<td>` + htmlEscape(r.Country) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", r.Online) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", r.Total) + `</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>

      <div class="card span-2">
        <h3>По серверам (только за период)</h3>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Сервер</th>
                <th>Страна</th>
                <th>Онлайн (сумма)</th>
                <th>Всего (сумма)</th>
              </tr>
            </thead>
            <tbody>
`)
	if len(panelRows) == 0 {
		b.WriteString(`<tr><td colspan="5" style="color:var(--muted);">Нет данных</td></tr>`)
	} else {
		for _, r := range panelRows {
			b.WriteString(`<tr>`)
			b.WriteString(`<td class="mono">` + fmt.Sprintf("%d", r.PanelID) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Name) + `</td>`)
			b.WriteString(`<td>` + htmlEscape(r.Country) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", r.Online) + `</td>`)
			b.WriteString(`<td>` + fmt.Sprintf("%d", r.Total) + `</td>`)
			b.WriteString(`</tr>`)
		}
	}
	b.WriteString(`</tbody></table></div></div>

    </div>
  </section>
`)

	b.WriteString(`
  </main>
</div>
<script>
  const links = document.querySelectorAll('.nav a');
  const sections = document.querySelectorAll('.section');
  function activate(id){
    sections.forEach(s => s.classList.toggle('active', s.id === id));
    links.forEach(a => a.classList.toggle('active', a.dataset.section === id));
  }
  function byHash(){
    const params = new URLSearchParams(location.search);
    const tab = params.get('tab');
    const base = location.hash || (tab ? '#'+tab : '#servers');
    const id = base.substring(1);
    activate(id);
  }
  links.forEach(a => a.addEventListener('click', e => {
    const id = a.dataset.section;
    if (id){ e.preventDefault(); history.replaceState(null,'','#'+id); activate(id); }
  }));
  window.addEventListener('hashchange', byHash);
  byHash();
</script>
`)

	return b.String()
}

// ======== restored missing handlers from server/main.go ========
func formatMoney(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// ======================= AUTH/API ======================

func sendCode(c *fiber.Ctx) error {
	type Req struct {
		Email string `json:"email"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Некорректные данные"})
	}
	r.Email = normEmail(r.Email)
	if r.Email == "" || !strings.Contains(r.Email, "@") {
		return c.Status(400).JSON(fiber.Map{"error": "Введите корректную почту"})
	}

	// rate-limit per email (дополнительно к IP-лимитеру)
	var lastCreated string
	_ = db.QueryRow("SELECT created_at FROM email_codes WHERE email=? ORDER BY id DESC LIMIT 1", r.Email).Scan(&lastCreated)
	if lastCreated != "" {
		if t, err := time.Parse(time.RFC3339, lastCreated); err == nil && now().Sub(t) < 60*time.Second {
			return c.Status(429).JSON(fiber.Map{"error": "Подождите 60 секунд перед повторной отправкой"})
		}
	}

	// уже есть пользователь?
	var existingUserID int64
	err := db.QueryRow("SELECT id FROM users WHERE email=?", r.Email).Scan(&existingUserID)
	isNew := err == sql.ErrNoRows

	// генерим OTP
	code := randomDigits(6)
	n := now()
	expires := n.Add(30 * time.Minute).Format(time.RFC3339)
	db.Exec("INSERT INTO email_codes (email, code, created_at, expires_at) VALUES (?,?,?,?)",
		r.Email, code, n.Format(time.RFC3339), expires)

	// если новый пользователь — готовим пробный промокод (14 дней) без округлений
	trialSection := ""
	redeemButtonURL := ""
	if isNew {
		monthly := getGlobalMonthlyPrice()
		trialAmount := monthly / 30.0 * 14.0

		trialCode := genPromoCode()
		trialExpires := now().Add(30 * 24 * time.Hour).Format(time.RFC3339)

		_, err := db.Exec("INSERT INTO promo_codes(code,amount,max_uses,expires_at,created_by) VALUES(?,?,?,?,?)",
			trialCode, trialAmount, 1, trialExpires, "system-trial")
		if err != nil {
			log.Println("trial promo insert error:", err)
		} else {
			trialSection = fmt.Sprintf(`
        <div style="margin-top:18px;padding:14px;border:1px solid %s;border-radius:14px;background:%s">
          <div style="color:%s;font-size:14px;font-weight:700;margin-bottom:6px;">Подарок на 14 дней</div>
          <div style="color:%s;font-size:13px;margin-bottom:8px;">
            Мы начислим на баланс сумму за <b>14 дней</b> использования (%s ₽).
          </div>
          %s
        </div>
      `, brandBorder, brandCard, brandText, brandMuted, formatMoney(trialAmount), buildCodeBox("Промокод", trialCode))
			redeemButtonURL = "https://offlag.app/redeem?code=" + trialCode
		}
	}

	// письмо
	preheader := "Ваш код входа в OffLag — действует 30 минут."
	title := "Код входа в OffLag"
	lead := "Введите этот код в приложении, чтобы войти. Код действует <b>30 минут</b>."
	otpBlock := buildCodeBox("Код для входа", code)
	html := buildBrandEmailHTML(preheader, title, lead, otpBlock+trialSection, func() string {
		if redeemButtonURL != "" {
			return "Активировать промокод"
		}
		return ""
	}(), redeemButtonURL)

	if err := sendEmail(r.Email, "Код для входа — OffLag", html); err != nil {
		log.Println("SMTP error:", err)
		return c.Status(500).JSON(fiber.Map{"error": "Не удалось отправить письмо"})
	}
	return c.JSON(fiber.Map{"message": "Код отправлен"})
}

func verifyCode(c *fiber.Ctx) error {
	type Req struct {
		Email  string `json:"email"`
		Code   string `json:"code"`
		Device string `json:"device"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Некорректные данные"})
	}
	r.Email = normEmail(r.Email)
	r.Device = clampDeviceName(r.Device)
	if r.Email == "" || r.Code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Поля не должны быть пустыми"})
	}

	var expires string
	err := db.QueryRow("SELECT expires_at FROM email_codes WHERE email=? AND code=?", r.Email, r.Code).Scan(&expires)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Неверный код"})
	}
	if t, _ := time.Parse(time.RFC3339, expires); now().After(t) {
		return c.Status(400).JSON(fiber.Map{"error": "Код истёк"})
	}
	db.Exec("DELETE FROM email_codes WHERE email=?", r.Email)

	var userID int64
	newUser := false
	err = db.QueryRow("SELECT id FROM users WHERE email=?", r.Email).Scan(&userID)
	if err == sql.ErrNoRows {
		newUser = true
		nowS := nowStr()
		res, insErr := db.Exec(
			"INSERT INTO users(email, nickname, balance, created_at, last_login) VALUES(?,?,?,?,?)",
			r.Email, nil, 0.0, nowS, nowS,
		)
		if insErr != nil {
			if err2 := db.QueryRow("SELECT id FROM users WHERE email=?", r.Email).Scan(&userID); err2 != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Ошибка создания пользователя"})
			}
			newUser = false
		} else {
			userID, _ = res.LastInsertId()
		}
	} else if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка базы данных"})
	}

	var deviceID int64
	_ = db.QueryRow("SELECT id FROM devices WHERE user_id=? AND device_name=?", userID, r.Device).Scan(&deviceID)
	if deviceID == 0 {
		res, _ := db.Exec("INSERT INTO devices(user_id, device_name, user_agent, last_seen_at) VALUES(?,?,?,?)",
			userID, r.Device, "", nowStr())
		deviceID, _ = res.LastInsertId()
	} else {
		db.Exec("UPDATE devices SET last_seen_at=? WHERE id=?", nowStr(), deviceID)
	}

	db.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND device_id=? AND revoked_at IS NULL",
		nowStr(), userID, deviceID)

	token := createJWT(r.Email)
	ip := c.IP()
	expiresAt := addDurStr(accessTokenTTL)
	db.Exec("INSERT INTO sessions(user_id, device_id, token, ip, expires_at) VALUES(?,?,?,?,?)",
		userID, deviceID, token, ip, expiresAt)

	refreshToken, _, refreshErr := saveRefreshToken(userID, &deviceID, ip)

	db.Exec("INSERT INTO auth_events(user_id,email,device_id,device,ip,event) VALUES(?,?,?,?,?,?)",
		userID, r.Email, deviceID, r.Device, ip, "login_success")
	db.Exec("UPDATE users SET last_login=? WHERE id=?", nowStr(), userID)

	resp := fiber.Map{"new_user": newUser, "token": token}
	if refreshErr == nil {
		resp["refresh_token"] = refreshToken
	}
	return c.JSON(resp)
}

func setNickname(c *fiber.Ctx) error {
	type Req struct {
		Nickname string `json:"nickname"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid data"})
	}
	r.Nickname = strings.TrimSpace(r.Nickname)
	if r.Nickname == "" {
		return c.Status(400).JSON(fiber.Map{"error": "nickname required"})
	}
	if ln := len(r.Nickname); ln < 3 || ln > 24 {
		return c.Status(400).JSON(fiber.Map{"error": "nickname must be 3-24 chars"})
	}
	userID, _ := c.Locals("user_id").(int64)
	email := c.Locals("email").(string)
	if userID == 0 || email == "" {
		return c.Status(401).JSON(fiber.Map{"error": "auth required"})
	}
	var current sql.NullString
	if err := db.QueryRow("SELECT nickname FROM users WHERE id=?", userID).Scan(&current); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if current.Valid && strings.TrimSpace(current.String) != "" {
		return c.Status(400).JSON(fiber.Map{"error": "nickname already set"})
	}
	var exists int
	_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE nickname=? AND id<>?", r.Nickname, userID).Scan(&exists)
	if exists > 0 {
		return c.Status(400).JSON(fiber.Map{"error": "nickname already taken"})
	}

	if _, err := db.Exec("UPDATE users SET nickname=?, updated_at=? WHERE id=?", r.Nickname, nowStr(), userID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "nickname update failed"})
	}

	// sync user to all panels once nickname is set
	syncNewUserToAllPanels(userID, email, r.Nickname)

	resp := fiber.Map{"message": "OK", "nickname": r.Nickname}
	if t := rawAuthToken(c); t != "" {
		resp["token"] = t
	}
	return c.JSON(resp)
}

func getProfile(c *fiber.Ctx) error {
	email := c.Locals("email").(string)

	var (
		id            int64
		nickname      sql.NullString
		balance       float64
		priceOverride sql.NullFloat64
		createdAt     sql.NullString
		lastLogin     sql.NullString
	)
	err := db.QueryRow(`SELECT id, nickname, balance, price_override, created_at, last_login
		FROM users WHERE email=?`, email).Scan(&id, &nickname, &balance, &priceOverride, &createdAt, &lastLogin)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Пользователь не найден"})
	}

	globalMonthly := getGlobalMonthlyPrice()
	effectivePrice := globalMonthly
	if priceOverride.Valid && priceOverride.Float64 > 0 {
		effectivePrice = priceOverride.Float64
	}

	var activeSessions int
	_ = db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=? AND revoked_at IS NULL AND expires_at>?",
		id, nowStr()).Scan(&activeSessions)

	premium := getPremiumInfoForUser(id)
	payments := fiber.Map{
		"yookassa_enabled": yooEnabled && yooClient != nil,
	}

	return c.JSON(fiber.Map{
		"id":              id,
		"email":           email,
		"nickname":        strOr(nickname, ""),
		"balance":         balance,
		"created_at":      strOr(createdAt, ""),
		"last_login":      strOr(lastLogin, ""),
		"plan":            map[string]any{"id": 0, "code": "", "name": ""},
		"price_override":  nullFloat(priceOverride),
		"monthly_price":   globalMonthly,
		"effective_price": effectivePrice,
		"active_sessions": activeSessions,
		"premium": fiber.Map{
			"active":     premium.Active,
			"users_left": premium.UsersLeft,
			"days_left":  premium.DaysLeft,
			"expires_at": premium.ExpiresAt,
		},
		"payments": payments,
	})
}

func logoutCurrent(c *fiber.Ctx) error {
	token := rawAuthToken(c)
	if token == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Нет токена"})
	}
	db.Exec("UPDATE sessions SET revoked_at=? WHERE token=?", nowStr(), token)
	if uid, ok := c.Locals("user_id").(int64); ok {
		if did, ok := c.Locals("device_id").(int64); ok {
			revokeRefreshTokens(uid, &did)
		} else {
			revokeRefreshTokens(uid, nil)
		}
	}
	return c.JSON(fiber.Map{"message": "Вы вышли на этом устройстве"})
}

func logoutAll(c *fiber.Ctx) error {
	email := c.Locals("email").(string)
	var userID int64
	_ = db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID)
	db.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=?", nowStr(), userID)
	revokeRefreshTokens(userID, nil)
	return c.JSON(fiber.Map{"message": "Вы вышли на всех устройствах"})
}

// refresh token -> new access + refresh
func refreshAuth(c *fiber.Ctx) error {
	type Req struct {
		RefreshToken string `json:"refresh_token"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || strings.TrimSpace(r.RefreshToken) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "refresh_token required"})
	}

	hash := hashToken(strings.TrimSpace(r.RefreshToken))

	tx, err := db.Begin()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db error"})
	}
	defer tx.Rollback()

	var userID int64
	var deviceID sql.NullInt64
	var email string
	err = tx.QueryRow(`
        SELECT rt.user_id, rt.device_id, u.email
        FROM refresh_tokens rt
        JOIN users u ON u.id = rt.user_id
        WHERE rt.token_hash=? AND rt.revoked_at IS NULL AND rt.expires_at>?
    `, hash, nowStr()).Scan(&userID, &deviceID, &email)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "invalid refresh_token"})
	}

	// rotate refresh token
	_, _ = tx.Exec("UPDATE refresh_tokens SET revoked_at=? WHERE token_hash=?", nowStr(), hash)

	ip := c.IP()
	newRefresh, _, err := func() (string, string, error) {
		raw := newRefreshToken()
		hashed := hashToken(raw)
		expiresAt := addDurStr(refreshTokenTTL)
		_, e := tx.Exec(
			"INSERT INTO refresh_tokens(user_id, device_id, token_hash, ip, expires_at) VALUES(?,?,?,?,?)",
			userID, deviceID, hashed, ip, expiresAt,
		)
		return raw, expiresAt, e
	}()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "refresh_token rotate failed"})
	}

	newAccess := createJWT(email)
	accessExpiresAt := addDurStr(accessTokenTTL)
	_, _ = tx.Exec("INSERT INTO sessions(user_id, device_id, token, ip, expires_at) VALUES(?,?,?,?,?)",
		userID, deviceID, newAccess, ip, accessExpiresAt)

	if err := tx.Commit(); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db commit error"})
	}

	return c.JSON(fiber.Map{
		"token":         newAccess,
		"refresh_token": newRefresh,
		"email":         email,
	})
}

func getAppVersion(c *fiber.Ctx) error {
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	if platform == "" {
		return c.Status(400).JSON(fiber.Map{"error": "platform required"})
	}
	vcStr := strings.TrimSpace(c.Query("version_code"))
	vc, err := strconv.Atoi(vcStr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "version_code required"})
	}

	var latestCode int
	var latestName, latestMsg, latestURL string
	var latestMin int
	err = db.QueryRow(`
        SELECT version_code, COALESCE(version_name,''), COALESCE(min_required,0),
               COALESCE(message,''), COALESCE(url,'')
        FROM app_versions
        WHERE platform=?
        ORDER BY version_code DESC
        LIMIT 1
    `, platform).Scan(&latestCode, &latestName, &latestMin, &latestMsg, &latestURL)
	if err == sql.ErrNoRows {
		return c.JSON(fiber.Map{
			"force_update":     false,
			"update_available": false,
			"min_required":     0,
			"latest_version":   "",
			"message":          "",
			"url":              "",
		})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db error"})
	}

	var maxMin int
	_ = db.QueryRow(`SELECT COALESCE(MAX(min_required),0) FROM app_versions WHERE platform=?`, platform).Scan(&maxMin)

	forceUpdate := vc < maxMin
	updateAvailable := vc < latestCode
	latestVersion := latestName
	if latestVersion == "" {
		latestVersion = fmt.Sprintf("%d", latestCode)
	}

	return c.JSON(fiber.Map{
		"force_update":     forceUpdate,
		"update_available": updateAvailable,
		"min_required":     maxMin,
		"latest_version":   latestVersion,
		"message":          latestMsg,
		"url":              latestURL,
	})
}

func premiumActivate(c *fiber.Ctx) error {
	type Req struct {
		Code string `json:"code"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || strings.TrimSpace(r.Code) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code required"})
	}
	code := strings.ToUpper(strings.TrimSpace(r.Code))
	email := c.Locals("email").(string)

	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}

	nowS := nowStr()
	var panelID int64
	var maxUsers int
	var expiresAt string
	var premiumUntil string
	err := db.QueryRow(
		`SELECT panel_id, max_users, expires_at, premium_until FROM premium_codes WHERE code=?`,
		code,
	).Scan(&panelID, &maxUsers, &expiresAt, &premiumUntil)
	if err == sql.ErrNoRows {
		return c.Status(400).JSON(fiber.Map{"error": "code not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db error"})
	}

	if t, e := time.Parse(time.RFC3339, expiresAt); e == nil && now().After(t) {
		return c.Status(400).JSON(fiber.Map{"error": "code expired"})
	}
	if t, e := time.Parse(time.RFC3339, premiumUntil); e == nil && now().After(t) {
		return c.Status(400).JSON(fiber.Map{"error": "premium expired"})
	}

	var activeUsers int
	_ = db.QueryRow(`SELECT COUNT(*) FROM premium_memberships WHERE code=? AND expires_at>?`, code, nowS).Scan(&activeUsers)
	if activeUsers >= maxUsers {
		return c.Status(400).JSON(fiber.Map{"error": "code user limit reached"})
	}

	if _, err := db.Exec(
		`INSERT OR IGNORE INTO premium_memberships(code,user_id,expires_at) VALUES(?,?,?)`,
		code, userID, premiumUntil,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "premium activate failed"})
	}

	syncUserToPanel(userID, panelID)
	info := getPremiumInfoForUser(userID)

	return c.JSON(fiber.Map{"message": "ok", "premium": info})
}

func getAnnouncementNext(c *fiber.Ctx) error {
	email := c.Locals("email").(string)
	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}

	nowS := nowStr()
	var (
		id       int64
		title    string
		body     string
		imageURL string
		ctaURL   string
	)
	err := db.QueryRow(`
        SELECT a.id, a.title, a.body, COALESCE(a.image_url,''), COALESCE(a.cta_url,'')
        FROM announcements a
        LEFT JOIN announcement_reads r ON r.announcement_id=a.id AND r.user_id=?
        WHERE (a.active_until IS NULL OR a.active_until > ?) AND r.id IS NULL
        ORDER BY a.created_at DESC
        LIMIT 1
    `, userID, nowS).Scan(&id, &title, &body, &imageURL, &ctaURL)
	if err == sql.ErrNoRows {
		return c.SendStatus(204)
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db error"})
	}
	return c.JSON(fiber.Map{
		"id":        id,
		"title":     title,
		"body":      body,
		"image_url": imageURL,
		"cta_url":   ctaURL,
	})
}

func markAnnouncementRead(c *fiber.Ctx) error {
	type Req struct {
		AnnouncementID int64 `json:"announcement_id"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || r.AnnouncementID <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "announcement_id required"})
	}
	email := c.Locals("email").(string)
	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO announcement_reads(announcement_id,user_id,read_at) VALUES(?,?,?)`,
		r.AnnouncementID, userID, nowStr(),
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db error"})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// promo redeem (user)

func redeemPromo(c *fiber.Ctx) error {
	type Req struct {
		Code string `json:"code"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || strings.TrimSpace(r.Code) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Введите промокод"})
	}
	email := c.Locals("email").(string)

	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Пользователь не найден"})
	}

	var amount float64
	var maxUses, usedCount int
	var exp sql.NullString
	err := db.QueryRow("SELECT amount, max_uses, used_count, expires_at FROM promo_codes WHERE code=?", r.Code).
		Scan(&amount, &maxUses, &usedCount, &exp)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Промокод не найден"})
	}
	if exp.Valid {
		if t, e := time.Parse(time.RFC3339, exp.String); e == nil && now().After(t) {
			return c.Status(400).JSON(fiber.Map{"error": "Промокод истёк"})
		}
	}
	if usedCount >= maxUses {
		return c.Status(400).JSON(fiber.Map{"error": "Промокод уже исчерпан"})
	}
	var exists int
	_ = db.QueryRow("SELECT COUNT(*) FROM promo_redemptions WHERE code=? AND user_id=?", r.Code, userID).Scan(&exists)
	if exists > 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Вы уже активировали этот код"})
	}

	tx, _ := db.Begin()
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE users SET balance = balance + ? WHERE id=?", amount, userID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка пополнения"})
	}
	if _, err := tx.Exec("INSERT INTO promo_redemptions(code,user_id,amount) VALUES(?,?,?)", r.Code, userID, amount); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка записи активации"})
	}
	if _, err := tx.Exec("UPDATE promo_codes SET used_count = used_count + 1 WHERE code=?", r.Code); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка обновления промокода"})
	}
	_ = tx.Commit()

	db.Exec("INSERT INTO auth_events(user_id,email,event) VALUES(?,?,?)", userID, email, "promo_redeem")
	return c.JSON(fiber.Map{"message": "Баланс пополнен", "amount": amount})
}

// ======================= YOOKASSA PAYMENTS ======================

func createYooKassaPayment(c *fiber.Ctx) error {
	if yooClient == nil {
		return c.Status(503).JSON(fiber.Map{
			"error": "Оплата временно недоступна (YooKassa не настроена на сервере)",
		})
	}

	type Req struct {
		Amount      float64 `json:"amount"`                // сумма пополнения в рублях
		Description string  `json:"description,omitempty"` // опционально
	}

	var r Req
	if err := c.BodyParser(&r); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Некорректный JSON"})
	}
	if r.Amount <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Сумма должна быть > 0"})
	}

	email := c.Locals("email").(string)

	// найдём user_id по email
	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Пользователь не найден"})
	}

	if r.Description == "" {
		r.Description = "Пополнение баланса OffLag"
	}

	// YooKassa ожидает сумму строкой с двумя знаками после запятой
	amountStr := formatMoney(r.Amount)

	// создаём платёж в YooKassa
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := yookassa.NewPaymentRequest{
		Description: r.Description,
		Capture:     true, // сразу списываем деньги после оплаты
		Amount: yookassa.Amount{
			Value:    amountStr,
			Currency: "RUB",
		},
		Confirmation: yookassa.Confirmation{
			Type:      "redirect",
			ReturnUrl: yooReturnURL,
		},
	}

	paymentResp, err := yooClient.NewPayment(ctx, req)
	if err != nil {
		log.Println("YooKassa NewPayment error:", err)
		return c.Status(502).JSON(fiber.Map{"error": "Ошибка создания платежа в YooKassa"})
	}

	// Сохраняем платёж в БД
	nowS := nowStr()
	_, err = db.Exec(`
        INSERT INTO payments(user_id, provider, provider_payment_id, amount, currency, status, created_at)
        VALUES(?,?,?,?,?,?,?)
    `,
		userID,
		"yookassa",
		paymentResp.Id,
		r.Amount,
		"RUB",
		paymentResp.Status, // обычно pending
		nowS,
	)
	if err != nil {
		log.Println("DB insert payment error:", err)
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка сохранения платежа"})
	}

	// ВНИМАНИЕ: в структуре NewPaymentResponse поле называется `Сonfirmation` с кириллической буквой 'С'
	confirmationURL := paymentResp.Сonfirmation.ConfirmationUrl

	return c.JSON(fiber.Map{
		"payment_id":       paymentResp.Id, // id в YooKassa
		"status":           paymentResp.Status,
		"confirmation_url": confirmationURL,
	})
}

// структура под тело вебхука от YooKassa
type yooKassaWebhookPayload struct {
	Event  string `json:"event"`
	Type   string `json:"type"`
	Object struct {
		Id     string `json:"id"`
		Status string `json:"status"`
		Paid   bool   `json:"paid"`
		Amount struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"amount"`
	} `json:"object"`
}

func yooKassaWebhook(c *fiber.Ctx) error {
	// защита по Basic Auth
	if !checkBasicAuth(c, yooWebhookUser, yooWebhookPass) {
		return c.Status(401).SendString("unauthorized")
	}

	body := c.Body()
	var w yooKassaWebhookPayload
	if err := json.Unmarshal(body, &w); err != nil {
		log.Println("YooKassa webhook JSON error:", err)
		return c.Status(400).SendString("bad json")
	}

	// Нас интересует только успешная оплата
	if w.Event != "payment.succeeded" {
		return c.SendStatus(200)
	}
	if w.Object.Status != "succeeded" || !w.Object.Paid {
		return c.SendStatus(200)
	}

	// Найдём платёж по id из YooKassa
	var (
		paymentID   int64
		userID      int64
		currentStat string
		amountDB    float64
		currencyDB  string
	)

	err := db.QueryRow(`
        SELECT id, user_id, status, amount, currency
        FROM payments
        WHERE provider = 'yookassa' AND provider_payment_id = ?
    `, w.Object.Id).Scan(&paymentID, &userID, &currentStat, &amountDB, &currencyDB)

	if err == sql.ErrNoRows {
		log.Println("YooKassa webhook: payment not found in DB:", w.Object.Id)
		return c.SendStatus(200)
	} else if err != nil {
		log.Println("YooKassa webhook DB error:", err)
		return c.Status(500).SendString("db error")
	}

	// Уже обработан? (idempotent)
	if currentStat == "succeeded" {
		return c.SendStatus(200)
	}

	if strings.ToUpper(currencyDB) != strings.ToUpper(w.Object.Amount.Currency) {
		log.Printf("YooKassa webhook: currency mismatch db=%s webhook=%s\n", currencyDB, w.Object.Amount.Currency)
	}

	amountWebhook, err := strconv.ParseFloat(strings.ReplaceAll(w.Object.Amount.Value, ",", "."), 64)
	if err != nil {
		log.Println("YooKassa webhook: amount parse error:", err)
		amountWebhook = amountDB
	}

	tx, txErr := db.Begin()
	if txErr != nil {
		log.Println("YooKassa webhook: tx begin error:", txErr)
		return c.Status(500).SendString("tx error")
	}
	defer tx.Rollback()

	nowS := nowStr()

	// Обновляем платёж
	if _, err := tx.Exec(`
        UPDATE payments
        SET status = ?, paid_at = ?, raw = ?
        WHERE id = ?
    `, "succeeded", nowS, string(body), paymentID); err != nil {
		log.Println("YooKassa webhook: update payment error:", err)
		return c.Status(500).SendString("update error")
	}

	// Пополняем баланс пользователя
	if _, err := tx.Exec(`
        UPDATE users
        SET balance = balance + ?
        WHERE id = ?
    `, amountWebhook, userID); err != nil {
		log.Println("YooKassa webhook: update user balance error:", err)
		return c.Status(500).SendString("update balance error")
	}

	if err := tx.Commit(); err != nil {
		log.Println("YooKassa webhook: tx commit error:", err)
		return c.Status(500).SendString("commit error")
	}

	log.Printf("YooKassa webhook: payment %s succeeded, user_id=%d, +%.2f RUB\n",
		w.Object.Id, userID, amountWebhook)

	return c.SendStatus(200)
}

// admin: per-user price override

func adminSetUserPrice(c *fiber.Ctx) error {
	type Req struct {
		Email         string   `json:"email"`
		PriceOverride *float64 `json:"price_override"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Неполные данные"})
	}
	r.Email = normEmail(r.Email)
	if r.Email == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Неполные данные"})
	}
	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", r.Email).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Пользователь не найден"})
	}
	if r.PriceOverride == nil {
		_, _ = db.Exec("UPDATE users SET price_override=NULL, updated_at=? WHERE id=?", nowStr(), userID)
	} else {
		if *r.PriceOverride <= 0 {
			return c.Status(400).JSON(fiber.Map{"error": "price_override должен быть > 0"})
		}
		_, _ = db.Exec("UPDATE users SET price_override=?, updated_at=? WHERE id=?", *r.PriceOverride, nowStr(), userID)
	}
	return c.JSON(fiber.Map{"message": "Ок"})
}

// admin: global monthly price

func adminSetMonthlyPrice(c *fiber.Ctx) error {
	type Req struct {
		Monthly float64 `json:"monthly"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || r.Monthly <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "monthly должен быть > 0"})
	}
	if err := setGlobalMonthlyPrice(r.Monthly); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "Цена обновлена", "monthly": r.Monthly})
}

func adminGetMonthlyPrice(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"monthly": getGlobalMonthlyPrice()})
}

func adminPromoCreate(c *fiber.Ctx) error {
	type Req struct {
		Amount  float64 `json:"amount"`
		MaxUses int     `json:"max_uses"`
		Expires string  `json:"expires_at,omitempty"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || r.Amount <= 0 || r.MaxUses <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Невалидные данные"})
	}
	code := genPromoCode()
	var exp *string
	if strings.TrimSpace(r.Expires) != "" {
		x := strings.TrimSpace(r.Expires)
		if _, err := time.Parse(time.RFC3339, x); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "expires_at должен быть RFC3339"})
		}
		exp = &x
	}
	adminEmail := c.Locals("email").(string)
	_, err := db.Exec("INSERT INTO promo_codes(code,amount,max_uses,expires_at,created_by) VALUES(?,?,?,?,?)",
		code, r.Amount, r.MaxUses, nullableStr(exp), adminEmail)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка создания промокода"})
	}
	return c.JSON(fiber.Map{"code": code, "amount": r.Amount, "max_uses": r.MaxUses, "expires_at": exp})
}

// ======================= EMAIL CHANGE ==================

func changeEmailRequest(c *fiber.Ctx) error {
	type Req struct {
		NewEmail string `json:"new_email"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || strings.TrimSpace(r.NewEmail) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Введите новую почту"})
	}
	oldEmail := strings.ToLower(c.Locals("email").(string))
	newEmail := normEmail(r.NewEmail)
	if newEmail == oldEmail {
		return c.Status(400).JSON(fiber.Map{
			"error": "Новая почта совпадает со старой",
		})
	}

	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", oldEmail).Scan(&userID); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Пользователь не найден"})
	}

	var exists int
	_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", newEmail).Scan(&exists)
	if exists > 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Эта почта уже используется"})
	}

	code := randomDigits(6)
	expires := addDurStr(30 * time.Minute)

	var hasRow int
	_ = db.QueryRow("SELECT COUNT(*) FROM pending_email_changes WHERE user_id=?", userID).Scan(&hasRow)
	if hasRow > 0 {
		_, _ = db.Exec("UPDATE pending_email_changes SET old_email=?, new_email=?, code=?, expires_at=? WHERE user_id=?",
			oldEmail, newEmail, code, expires, userID)
	} else {
		_, _ = db.Exec("INSERT INTO pending_email_changes(user_id, old_email, new_email, code, expires_at) VALUES(?,?,?,?,?)",
			userID, oldEmail, newEmail, code, expires)
	}

	preheader := "Подтвердите смену почты в OffLag — код действует 30 минут."
	title := "Подтверждение смены почты"
	lead := fmt.Sprintf("Вы хотите перенести аккаунт с <b>%s</b> на <b>%s</b>.<br>Введите этот код для подтверждения. Код действует <b>30 минут</b>.", oldEmail, newEmail)
	htmlBlock := buildCodeBox("Код подтверждения", code)
	html := buildBrandEmailHTML(preheader, title, lead, htmlBlock, "", "")
	if err := sendEmail(newEmail, "Подтверждение смены почты — OffLag", html); err != nil {
		log.Println("SMTP error:", err)
		return c.Status(500).JSON(fiber.Map{"error": "Не удалось отправить письмо на новую почту"})
	}
	return c.JSON(fiber.Map{"message": "Код подтверждения отправлен на новую почту"})
}

func changeEmailConfirm(c *fiber.Ctx) error {
	type Req struct {
		NewEmail string `json:"new_email"`
		Code     string `json:"code"`
	}
	var r Req
	if err := c.BodyParser(&r); err != nil || strings.TrimSpace(r.NewEmail) == "" || strings.TrimSpace(r.Code) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Введите почту и код"})
	}
	currentEmail := strings.ToLower(c.Locals("email").(string))
	newEmail := normEmail(r.NewEmail)
	code := strings.TrimSpace(r.Code)

	var rowUserID int64
	var dbOld, dbNew, dbExpire string
	if err := db.QueryRow(`SELECT user_id, old_email, new_email, expires_at
		FROM pending_email_changes
		WHERE new_email=? AND code=?`, newEmail, code).Scan(&rowUserID, &dbOld, &dbNew, &dbExpire); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Неверный код или почта"})
	}

	var userID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", currentEmail).Scan(&userID); err != nil || userID != rowUserID {
		return c.Status(403).JSON(fiber.Map{"error": "Недостаточно прав для подтверждения"})
	}
	if t, err := time.Parse(time.RFC3339, dbExpire); err == nil && now().After(t) {
		return c.Status(400).JSON(fiber.Map{"error": "Код истёк"})
	}

	var exists int
	_ = db.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", newEmail).Scan(&exists)
	if exists > 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Эта почта уже используется"})
	}

	tx, _ := db.Begin()
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE users SET email=?, updated_at=? WHERE id=?", newEmail, nowStr(), userID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка обновления почты"})
	}
	_, _ = tx.Exec("DELETE FROM pending_email_changes WHERE user_id=?", userID)
	ip := c.IP()
	_, _ = tx.Exec("INSERT INTO email_change_log(user_id, old_email, new_email, ip) VALUES(?,?,?,?)",
		userID, dbOld, dbNew, ip)
	if err := tx.Commit(); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Ошибка базы данных"})
	}

	db.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=?", nowStr(), userID)
	newToken := createJWT(newEmail)
	db.Exec("INSERT INTO sessions(user_id, token, ip, expires_at) VALUES(?,?,?,?)",
		userID, newToken, c.IP(), addDurStr(24*time.Hour))

	// уведомление на старую почту
	go func(oldEmail, newEmail string) {
		subject := "Почта аккаунта изменена — OffLag"
		pre := "Почта аккаунта была изменена."
		title := "Почта аккаунта изменена"
		lead := fmt.Sprintf("Аккаунт, ранее зарегистрированный на <b>%s</b>, теперь привязан к <b>%s</b>.", oldEmail, newEmail)
		note := `<div style="margin-top:12px;color:` + brandMuted + `;font-size:12px;">
Если это были не вы — срочно напишите: <a href="mailto:support@offlag.app" style="color:` + brandText + `;text-decoration:none;font-weight:700;">support@offlag.app</a>
</div>`
		html := buildBrandEmailHTML(pre, title, lead, note, "", "")
		_ = sendEmail(oldEmail, subject, html)
	}(dbOld, dbNew)

	return c.JSON(fiber.Map{"message": "Почта успешно изменена", "new_email": newEmail, "token": newToken})
}

// ======================= JWT & CLEANUP =================

func authMiddleware(c *fiber.Ctx) error {
	raw := rawAuthToken(c)
	if raw == "" {
		return c.Status(401).JSON(fiber.Map{"error": "auth required"})
	}
	token, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) { return jwtSecret, nil })
	if err != nil || !token.Valid {
		return c.Status(401).JSON(fiber.Map{"error": "invalid token"})
	}
	claims := token.Claims.(jwt.MapClaims)
	email, _ := claims["email"].(string)
	email = normEmail(email)

	var userID int64
	var deviceID sql.NullInt64
	if err := db.QueryRow(
		"SELECT user_id, device_id FROM sessions WHERE token=? AND revoked_at IS NULL AND expires_at>?",
		raw, nowStr(),
	).Scan(&userID, &deviceID); err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "session expired"})
	}

	c.Locals("email", email)
	c.Locals("user_id", userID)
	if deviceID.Valid {
		c.Locals("device_id", deviceID.Int64)
	}
	return c.Next()
}

func adminMiddleware(c *fiber.Ctx) error {
	email := strings.ToLower(c.Locals("email").(string))
	if !adminEmails[email] {
		return c.Status(403).JSON(fiber.Map{"error": "Требуются права администратора"})
	}
	return c.Next()
}

func createJWT(email string) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"email": email,
		"exp":   now().Add(accessTokenTTL).Unix(),
		"iat":   now().Unix(),
	})
	t, _ := tok.SignedString(jwtSecret)
	return t
}

func newRefreshToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func saveRefreshToken(userID int64, deviceID *int64, ip string) (string, string, error) {
	raw := newRefreshToken()
	hash := hashToken(raw)
	expiresAt := addDurStr(refreshTokenTTL)
	var dev sql.NullInt64
	if deviceID != nil && *deviceID > 0 {
		dev = sql.NullInt64{Int64: *deviceID, Valid: true}
	}
	_, err := db.Exec(
		"INSERT INTO refresh_tokens(user_id, device_id, token_hash, ip, expires_at) VALUES(?,?,?,?,?)",
		userID, dev, hash, ip, expiresAt,
	)
	if err != nil {
		return "", "", err
	}
	return raw, expiresAt, nil
}

func revokeRefreshTokens(userID int64, deviceID *int64) {
	if deviceID != nil && *deviceID > 0 {
		db.Exec("UPDATE refresh_tokens SET revoked_at=? WHERE user_id=? AND device_id=? AND revoked_at IS NULL",
			nowStr(), userID, *deviceID)
		return
	}
	db.Exec("UPDATE refresh_tokens SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL",
		nowStr(), userID)
}

func cleanupJob() {
	for {
		time.Sleep(24 * time.Hour)
		n := nowStr()
		db.Exec(`DELETE FROM email_codes WHERE expires_at < ?`, n)
		db.Exec(`DELETE FROM sessions WHERE (expires_at < ?) OR (revoked_at IS NOT NULL AND revoked_at < datetime(?, '-7 day'))`, n, n)
		db.Exec(`DELETE FROM refresh_tokens WHERE (expires_at < ?) OR (revoked_at IS NOT NULL AND revoked_at < datetime(?, '-30 day'))`, n, n)
		for k, exp := range adminUISessions {
			if now().After(exp) {
				delete(adminUISessions, k)
			}
		}
	}
}

// ======================= ADMIN UI (PASSWORD) ===========

// ======================= END ADMIN UI ==================
