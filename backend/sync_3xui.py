#!/usr/bin/env python3
"""
sync_3xui.py

Синхронизация пользователей OffLag в панели(ях) 3x-ui.

Формат входного JSON (stdin):

{
  "mode": "new_user" | "sync_panel",
  "panels": [
    {
      "id": 1,
      "name": "nl-1",
      "country": "NL",
      "base_url": "https://example.com:2053/randompath/",
      "login": "admin",
      "password": "secret",
      "enabled": true
      // inbound_id: необязателен, по умолчанию 1
    }
  ],
  "users": [
    {
      "id": 123,
      "email": "user@example.com",
      "nickname": "Nick"
    }
  ]
}

Скрипт:
- для каждой enabled-панели подключается к 3x-ui
- берёт inbound с указанным inbound_id (или 1, если не задан)
- смотрит inbound.settings.clients
- если client с таким email уже есть — пропускает
- если нет — создаёт нового клиента с этим email
- пишет в stdout JSON-результат
"""

import sys
import json
import uuid
from typing import Any, Dict, List

from py3xui import Api, Client  # pip install py3xui


def log_stderr(msg: str) -> None:
    """Простой логгер в stderr."""
    sys.stderr.write(msg + "\n")
    sys.stderr.flush()


def sync_panel(panel: Dict[str, Any], users: List[Dict[str, Any]]) -> Dict[str, Any]:
    """
    Синхронизирует список users в одну панель 3x-ui.
    """
    panel_id = panel.get("id")
    panel_name = panel.get("name", f"panel-{panel_id}")
    base_url = panel.get("base_url")

    # Go шлёт "login", но на всякий случай поддержим и "username"
    username = panel.get("login") or panel.get("username")
    password = panel.get("password")

    # inbound_id можем задать явно в JSON или использовать дефолт 1
    inbound_id = panel.get("inbound_id") or 1

    result = {
        "panel_id": panel_id,
        "name": panel_name,
        "added": [],     # список email'ов, которые были добавлены
        "skipped": [],   # список email'ов, которые уже были в inbound
        "errors": []     # список строк с ошибками
    }

    if not base_url or not username or not password:
        result["errors"].append("panel config incomplete (base_url/username/password)")
        return result

    try:
        api = Api(base_url, username, password)
        api.login()
    except Exception as e:
        result["errors"].append(f"login failed: {e}")
        return result

    try:
        inbound = api.inbound.get_by_id(inbound_id)
    except Exception as e:
        result["errors"].append(f"cannot get inbound {inbound_id}: {e}")
        return result

    existing_emails = {c.email for c in getattr(inbound.settings, "clients", []) or []}

    for u in users:
        email = (u.get("email") or "").strip().lower()
        if not email:
            continue

        if email in existing_emails:
            result["skipped"].append(email)
            continue

    try:
        # flow можно переопределять через panel["flow"], иначе используем xtls-rprx-vision
        flow = panel.get("flow") or "xtls-rprx-vision"

        client = Client(
            id=str(uuid.uuid4()),
            email=email,
            enable=True,
            flow=flow,
        )
        api.client.add(inbound_id, [client])
        result["added"].append(email)
        existing_emails.add(email)

    except Exception as e:
            msg = f"failed to add {email}: {e}"
            result["errors"].append(msg)
            log_stderr(f"[{panel_name}] {msg}")

    return result


def main() -> None:
    raw = sys.stdin.read()
    if not raw.strip():
        print(json.dumps({"ok": False, "error": "no input json"}, ensure_ascii=False))
        return

    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        print(json.dumps({"ok": False, "error": f"invalid json: {e}"}, ensure_ascii=False))
        return

    panels = data.get("panels") or []
    users = data.get("users") or []

    if not isinstance(panels, list) or not isinstance(users, list):
        print(json.dumps({"ok": False, "error": "panels and users must be lists"}, ensure_ascii=False))
        return

    overall = {
        "ok": True,
        "panels": []
    }

    for panel in panels:
        if not isinstance(panel, dict):
            continue
        if not panel.get("enabled", True):
            continue

        panel_result = sync_panel(panel, users)
        overall["panels"].append(panel_result)
        if panel_result["errors"]:
            overall["ok"] = False

    print(json.dumps(overall, ensure_ascii=False))


if __name__ == "__main__":
    main()
