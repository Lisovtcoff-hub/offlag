#!/usr/bin/env python3
"""
get_uuid_3xui.py

По email пользователя и данным панели(й) 3x-ui вытаскивает VLESS UUID
(поле `id` клиента в inbound.settings.clients).

Формат входного JSON (stdin):

{
  "email": "user@example.com",
  "panels": [
    {
      "id": 1,
      "name": "nl-1",
      "base_url": "https://vpn-panel.example.com/secret-path/",
      "login": "replace_with_panel_login",
      "password": "replace_with_panel_password",
      "inbound_id": 1
    }
  ]
}

Формат вывода:

{
  "ok": true/false,
  "email": "user@example.com",
  "results": [
    {
      "panel_id": 1,
      "name": "nl-1",
      "found": true/false,
      "uuid": "...." или "",
      "error": "строка ошибки или пусто"
    }
  ]
}
"""

import sys
import json
from typing import Any, Dict, List

from py3xui import Api  # pip install py3xui


def log_stderr(msg: str) -> None:
    sys.stderr.write(msg + "\n")
    sys.stderr.flush()


def find_uuid_in_panel(panel: Dict[str, Any], email: str) -> Dict[str, Any]:
    """
    Ищет клиента с email в указанной панели и возвращает его uuid (id клиента).
    """
    panel_id = panel.get("id")
    panel_name = panel.get("name", f"panel-{panel_id}")
    base_url = panel.get("base_url")

    username = panel.get("login") or panel.get("username")
    password = panel.get("password")
    inbound_id = panel.get("inbound_id") or 1

    result = {
        "panel_id": panel_id,
        "name": panel_name,
        "found": False,
        "uuid": "",
        "error": "",
    }

    if not base_url or not username or not password:
        result["error"] = "panel config incomplete (base_url/login/password)"
        return result

    try:
        api = Api(base_url, username, password)
        api.login()
    except Exception as e:
        msg = f"login failed: {e}"
        log_stderr(f"[{panel_name}] {msg}")
        result["error"] = msg
        return result

    try:
        inbound = api.inbound.get_by_id(inbound_id)
    except Exception as e:
        msg = f"cannot get inbound {inbound_id}: {e}"
        log_stderr(f"[{panel_name}] {msg}")
        result["error"] = msg
        return result

    clients = getattr(inbound.settings, "clients", []) or []

    target = email.strip().lower()
    if not target:
        result["error"] = "empty email"
        return result

    for c in clients:
        c_email = (getattr(c, "email", "") or "").strip().lower()
        if c_email == target:
            # В 3x-ui именно поле id является UUID для VLESS клиента
            uuid_str = getattr(c, "id", "") or ""
            uuid_str = str(uuid_str).strip()
            if not uuid_str:
                result["error"] = "client found but id/uuid is empty"
                return result

            result["found"] = True
            result["uuid"] = uuid_str
            return result

    result["error"] = f"client with this email not found in inbound {inbound_id}"
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

    email = (data.get("email") or "").strip().lower()
    panels = data.get("panels") or []

    if not email:
        print(json.dumps({"ok": False, "error": "email is required"}, ensure_ascii=False))
        return

    if not isinstance(panels, list) or not panels:
        print(json.dumps({"ok": False, "error": "panels must be non-empty list"}, ensure_ascii=False))
        return

    results: List[Dict[str, Any]] = []
    overall_ok = True

    for panel in panels:
        if not isinstance(panel, dict):
            continue
        r = find_uuid_in_panel(panel, email)
        results.append(r)
        if r["error"]:
            overall_ok = False

    out = {
        "ok": overall_ok,
        "email": email,
        "results": results,
    }
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main()
