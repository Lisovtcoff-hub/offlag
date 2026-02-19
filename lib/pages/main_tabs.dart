import 'dart:async';
import 'dart:io' show Platform, exit;

import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:url_launcher/url_launcher.dart';
import '../theme.dart';
import '../widgets/widgets.dart';
import '../widgets/native_loading_overlay.dart';
import '../net.dart';
import '../models/user_profile.dart';
import '../models/vpn_node.dart';
import '../vpn_controller.dart';
import 'home_screen.dart';
import 'profile_screen.dart';
import 'servers.dart';

class MainTabs extends StatefulWidget {
  const MainTabs({super.key});

  @override
  State<MainTabs> createState() => _MainTabsState();
}

class _MainTabsState extends State<MainTabs> with TickerProviderStateMixin {
  static const List<String> _kAllowedExternalDomains = [
    'play.google.com',
    'play.app.goo.gl',
    'api.lisovcoff.ru',
    'offlag.ru',
  ];
  int _index = 0;
  bool _connected = false;

  late final PageController _pageCtrl = PageController(initialPage: _index);

  UserProfile? _me;
  bool _loadingMe = false;

  VpnNode? _bestNode;
  int? _bestPingMs;
  bool _connectBusy = false;
  String _connectBusyLabel = 'Поиск лучшего сервера и подключение...';
  bool _startupChecksDone = false;

  /// Универсальный контроллер VPN (Windows + Android).
  final VpnController _vpn = VpnController();

  Timer? _pingTimer;

  @override
  void initState() {
    super.initState();
    _fetchMe();
    _loadVpn();
    _startPingTimer();

    // Реактивно обновляем UI при изменении состояния VPN.
    _vpn.status.addListener(() {
      if (!mounted) return;
      setState(() => _connected = _vpn.isRunning);
    });
  }

  @override
  void dispose() {
    _pingTimer?.cancel();
    _pageCtrl.dispose();

    // На Windows выключаем при закрытии UI; на Android оставляем сервис работать.
    if (Platform.isWindows) {
      _vpn.disconnect();
    }

    super.dispose();
  }

  Future<void> _fetchMe() async {
    setState(() => _loadingMe = true);
    try {
      final res = await dio.get('/profile');
      final data = res.data is Map
          ? (res.data as Map<String, dynamic>)
          : <String, dynamic>{};
      setState(() => _me = UserProfile.fromMap(data));
      if (!_startupChecksDone) {
        _startupChecksDone = true;
        unawaited(_runStartupChecks());
      }
    } catch (e, st) {
      debugPrint('GET /profile failed: $e\n$st');
    } finally {
      if (mounted) setState(() => _loadingMe = false);
    }
  }

  Future<void> _runStartupChecks() async {
    await _checkVersion();
    await _checkAnnouncement();
  }

  Future<void> _checkVersion() async {
    if (!Platform.isAndroid && !Platform.isIOS) return;
    final info = await PackageInfo.fromPlatform();
    final build = int.tryParse(info.buildNumber) ?? 0;
    final platform = Platform.isAndroid ? 'android' : 'ios';
    final data = await checkAppVersion(platform: platform, versionCode: build);
    final forceUpdate = data['force_update'] == true;
    final updateAvailable = data['update_available'] == true;
    if ((!forceUpdate && !updateAvailable) || !mounted) return;

    final message = (data['message'] as String?)?.trim();
    final url = (data['url'] as String?)?.trim();
    await showDialog<void>(
      context: context,
      barrierDismissible: !forceUpdate,
      builder: (ctx) => NativeDialogFrame(
        title: Text(
          forceUpdate ? 'Требуется обновление' : 'Доступно обновление',
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
        content: Text(
          message?.isNotEmpty == true
              ? message!
              : (forceUpdate
                    ? 'Для продолжения работы обновите приложение.'
                    : 'Рекомендуем обновить приложение до последней версии.'),
        ),
        actions: [
          if (!forceUpdate)
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Позже'),
            ),
          TextButton(
            onPressed: () async {
              if (url != null && url.isNotEmpty) {
                final normalized = _normalizeUrl(url);
                if (normalized != null) {
                  await launchUrl(
                    normalized,
                    mode: LaunchMode.externalApplication,
                  );
                }
              }
            },
            child: const Text('Перейти'),
          ),
        ],
      ),
    );
  }

  Future<void> _checkAnnouncement() async {
    final ann = await fetchAnnouncementNext();
    if (!mounted || ann == null) return;
    final id = (ann['id'] as int?) ?? 0;
    final title = (ann['title'] as String?) ?? '';
    final body = (ann['body'] as String?) ?? '';
    final imageUrl = (ann['image_url'] as String?) ?? '';
    final ctaUrl = (ann['cta_url'] as String?) ?? '';

    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => NativeDialogFrame(
        title: Text(
          title.isEmpty ? 'Объявление' : title,
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
        onClose: () async {
          Navigator.pop(ctx);
          if (id > 0) await markAnnouncementRead(id);
        },
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (imageUrl.isNotEmpty) ...[
              const SizedBox(height: 6),
              ClipRRect(
                borderRadius: BorderRadius.circular(12),
                child: Image.network(imageUrl),
              ),
            ],
            if (body.isNotEmpty) ...[const SizedBox(height: 10), Text(body)],
          ],
        ),
        actions: [
          if (ctaUrl.isNotEmpty)
            TextButton(
              onPressed: () async {
                Navigator.pop(ctx);
                if (id > 0) await markAnnouncementRead(id);
                final normalized = _normalizeUrl(ctaUrl);
                if (normalized != null) {
                  await launchUrl(
                    normalized,
                    mode: LaunchMode.externalApplication,
                  );
                }
              },
              child: const Text('Перейти'),
            ),
        ],
      ),
    );
  }

  bool _isAllowedHost(String host) {
    if (host.isEmpty) return false;
    final h = host.toLowerCase();
    for (final d in _kAllowedExternalDomains) {
      if (h == d || h.endsWith('.$d')) return true;
    }
    return false;
  }

  Uri? _normalizeUrl(String raw) {
    final trimmed = raw.trim();
    if (trimmed.isEmpty) return null;
    final withScheme = trimmed.contains('://') ? trimmed : 'https://$trimmed';
    final uri = Uri.tryParse(withScheme);
    if (uri == null) return null;
    if (uri.scheme != 'https') return null;
    if (!_isAllowedHost(uri.host)) {
      debugPrint('Blocked external URL: ${uri.host}');
      return null;
    }
    return uri;
  }

  Future<void> _loadVpn({bool fast = false}) async {
    try {
      final nodes = await fetchVpnNodes();
      final result = await findBestNode(
        nodes,
        timeout: fast ? const Duration(seconds: 1) : const Duration(seconds: 2),
        tries: fast ? 1 : 3,
      );
      if (!mounted) return;

      setState(() {
        _bestNode = result.best;
        _bestPingMs = result.best != null
            ? result.pingsMs[result.best!.id]
            : null;
      });
    } catch (_) {
      // тихо падаем
    }
  }

  Future<void> _refreshPing() async {
    final node = _bestNode;
    if (node == null) return;

    final hp = hostPortFromNode(node);
    if (hp == null) return;
    final ping = await measureTcpPing(hp.host, hp.port);
    if (!mounted) return;
    if (ping != null) {
      setState(() {
        _bestPingMs = ping;
      });
    }
  }

  Future<void> _disconnectVpnForLogout() async {
    if (!_connected) return;
    await _vpn.disconnect();
    if (!mounted) return;
    setState(() => _connected = false);
  }

  void _startPingTimer() {
    _pingTimer?.cancel();
    _pingTimer = Timer.periodic(const Duration(minutes: 1), (_) async {
      if (!_connected) return;
      await _refreshPing();
    });
  }

  /// Переключает VPN: запускает или останавливает подключение.
  ///
  /// Детали платформы инкапсулированы в [VpnController]:
  /// - Windows: xray.exe (TUN)
  /// - Android: VpnService + flutter_v2ray_plus
  void _toggleConnection() async {
    if (_connectBusy) return;

    // Disconnect
    if (_connected) {
      setState(() {
        _connectBusy = true;
        _connectBusyLabel = 'Отключение VPN...';
      });
      await _vpn.disconnect();
      if (mounted) setState(() => _connectBusy = false);

      if (mounted) {
        setState(() => _connected = false);
      }
      return;
    }

    // Connect using the last selected node without waiting for pings.
    setState(() {
      _connectBusy = true;
      _connectBusyLabel = 'Поиск лучшего сервера и подключение...';
    });
    var node = _bestNode;
    if (node == null) {
      try {
        final nodes = await fetchVpnNodes();
        if (nodes.isNotEmpty) {
          node = nodes.first;
          _bestNode = node;
        }
      } catch (_) {}
    }

    if (node == null) {
      if (mounted) setState(() => _connectBusy = false);
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Нет доступных серверов')));
      return;
    }

    final ok = await _vpn.connect(node);
    if (mounted) setState(() => _connectBusy = false);

    if (!mounted) return;

    if (ok) {
      setState(() => _connected = true);
    } else {
      setState(() => _connected = false);
      if (Platform.isWindows && _vpn.windowsRequiresAdmin) {
        await _showRequiresAdminDialog();
        return;
      }
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Не удалось запустить VPN')));
    }

    // Refresh best node in background without blocking the user.
    unawaited(_loadVpn(fast: true));
  }

  /// Открывает экран "Серверы", позволяет выбрать ноду и подключиться к ней.
  Future<void> _openServers(BuildContext context) async {
    if (_connectBusy) return;
    final selected = await Navigator.of(context).push<VpnNode>(
      MaterialPageRoute(
        builder: (_) => ServersPage(includeAll: _me?.premiumActive ?? false),
      ),
    );

    if (!mounted || selected == null) return;
    if (_connectBusy) return;
    setState(() {
      _bestNode = selected;
      _bestPingMs = selected.pingMs ?? _bestPingMs;
    });

    setState(() {
      _connectBusy = true;
      _connectBusyLabel = 'Подключение к выбранному серверу...';
    });
    bool ok = false;
    try {
      // Disconnect if already connected.
      if (_connected) {
        setState(() => _connectBusyLabel = 'Отключение VPN...');
        await _vpn.disconnect();
        if (!mounted) return;
        setState(() => _connected = false);
        // Небольшая задержка, чтобы дать время на корректное отключение.
        await Future.delayed(const Duration(milliseconds: 700));
      }

      if (mounted) {
        setState(() => _connectBusyLabel = 'Подключение к выбранному серверу...');
      }
      ok = await _vpn.connect(selected);
    } finally {
      if (mounted) setState(() => _connectBusy = false);
    }

    if (!context.mounted) return;

    if (ok) {
      setState(() => _connected = true);
    } else {
      if (Platform.isWindows && _vpn.windowsRequiresAdmin) {
        await _showRequiresAdminDialog();
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Не удалось подключиться к выбранному серверу'),
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final nodeForUi = _bestNode;
    final countryLabel = nodeForUi?.name ?? 'Netherlands';
    final countryCode = nodeForUi?.country ?? 'NL';
    final pingMs = _bestPingMs ?? 58;

    final pages = [
      HomeScreen(
        connected: _connected,
        onToggleConnection: _toggleConnection,
        me: _me,
        loadingMe: _loadingMe,
        onRefreshMe: _fetchMe,
        countryLabel: countryLabel,
        countryCode: countryCode,
        pingMs: pingMs,
        onOpenServers: _openServers,
      ),
      ProfileScreen(
        me: _me,
        loadingMe: _loadingMe,
        onRefreshMe: _fetchMe,
        onDisconnectVpn: _disconnectVpnForLogout,
      ),
    ];

    return Stack(
      children: [
        AbsorbPointer(
          absorbing: _connectBusy,
          child: Scaffold(
            body: SafeArea(
              child: PageView(
                controller: _pageCtrl,
                physics: const BouncingScrollPhysics(),
                onPageChanged: (i) => setState(() => _index = i),
                children: pages,
              ),
            ),
            bottomNavigationBar: SafeArea(
              minimum: const EdgeInsets.fromLTRB(16, 0, 16, 16),
              child: SizedBox(
                height: Ui.ovalH,
                child: BottomOvalNav(
                  currentIndex: _index,
                  onTap: (i) {
                    setState(() => _index = i);
                    _pageCtrl.animateToPage(
                      i,
                      duration: const Duration(milliseconds: 220),
                      curve: Curves.easeInOut,
                    );
                  },
                ),
              ),
            ),
          ),
        ),
        if (_connectBusy)
          Positioned.fill(
            child: NativeLoadingOverlay(
              label: _connectBusyLabel,
            ),
          ),
      ],
    );
  }

  Future<void> _showRequiresAdminDialog() async {
    if (!mounted) return;
    await showDialog<void>(
      context: context,
      barrierDismissible: true,
      builder: (ctx) => NativeDialogFrame(
        title: const Text(
          'Нужны права администратора',
          style: TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
        content: const Text(
          'Для настройки маршрутов VPN приложение нужно перезапустить с правами администратора.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Отмена'),
          ),
          TextButton(
            onPressed: () async {
              Navigator.pop(ctx);
              final started = await VpnController.relaunchAsAdmin();
              if (started) {
                exit(0);
              }
              if (!mounted) return;
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text('Не удалось запустить приложение с правами администратора'),
                ),
              );
            },
            child: const Text('Перезапустить'),
          ),
        ],
      ),
    );
  }
}



