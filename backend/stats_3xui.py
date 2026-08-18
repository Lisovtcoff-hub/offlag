#!/usr/bin/env python3
"""
stats_3xui.py

Собирает простую статистику по панелям 3x-ui.

Формат входного JSON (stdin):

{
  "panels": [
    {
      "id": 1,
      "name": "nl-test",
      "country": "NL",
      "base_url": "https://vpn-panel.example.com/secret-path/",
      "login": "replace_with_panel_login",
      "password": "replace_with_panel_password",
      "inbound_id": 1,
      "enabled": true
    }
  ]
}

Формат вывода:

{
  "ok": true,
  "panels": [
    {
      "panel_id": 1,
      "name": "nl-test",
      "online": N,
      "total": M,
      "errors": []
    }
  ]
}
"""

import sys
import json
from typing import Any, Dict, List, Optional

import requests
from py3xui import Api  # pip install py3xui


def log_err(msg: str) -> None:
    sys.stderr.write(msg + "\n")
    sys.stderr.flush()


def _normalize_base(base_url: str) -> str:
    """
    Нормализуем base_url: убираем лишние слэши в конце.
    Пример: 'https://host.example/secret-path/' -> 'https://host.example/secret-path'
    """
    return base_url.rstrip("/")


def login_raw_session(base_url: str, username: str, password: str) -> Optional[requests.Session]:
    """
    Логинимся в 3x-ui «вручную» через requests, чтобы получить cookie.
    Используем тот же base_url, что и для py3xui.

    Обычно форма логина находится по адресу: <base_url>/login
    (3x-ui сам уже знает, где /panel/api/... и т.п.)
    """
    base = _normalize_base(base_url)
    login_url = f"{base}/login"

    s = requests.Session()
    try:
        resp = s.post(
            login_url,
            data={"username": username, "password": password},
            timeout=10,
        )
    except Exception as e:
        log_err(f"[login_raw_session] login request failed: {e}")
        return None

    if resp.status_code != 200:
        log_err(f"[login_raw_session] login status {resp.status_code}, body={resp.text[:200]}")
        return None

    return s


def get_online_count(base_url: str, session: requests.Session, inbound_id: int) -> int:
    """
    Пытается получить список online-клиентов по разным возможным endpoint'ам
    3x-ui. Возвращает количество online-клиентов, либо 0 при ошибке.
    """
    base = _normalize_base(base_url)

    candidate_paths = [
        "/panel/api/inbounds/onlines",
        "/panel/api/inbound/onlines",
        "/xui/inbound/onlines",
        "/xui/api/inbounds/onlines",
        "/onlines",
    ]

    payload = {"inboundId": inbound_id}
    headers = {"Accept": "application/json"}

    for path in candidate_paths:
        url = f"{base}{path}"
        try:
            resp = session.post(url, json=payload, headers=headers, timeout=10)
        except Exception as e:
            log_err(f"[get_online_count] request {url} failed: {e}")
            continue

        if resp.status_code != 200:
            log_err(f"[get_online_count] {url} -> status {resp.status_code}")
            continue

        try:
            data = resp.json()
        except Exception as e:
            log_err(f"[get_online_count] {url} json error: {e}, body={resp.text[:200]}")
            continue

        if isinstance(data, list):
            return len(data)

        if isinstance(data, dict):
            obj = data.get("obj")
            if isinstance(obj, list):
                return len(obj)

            if isinstance(data.get("clients"), list):
                return len(data["clients"])
            if isinstance(data.get("onlines"), list):
                return len(data["onlines"])

        log_err(f"[get_online_count] {url} unexpected json: {str(data)[:200]}")

    return 0


def collect_panel_stats(panel: Dict[str, Any]) -> Dict[str, Any]:
    panel_id = panel.get("id")
    panel_name = panel.get("name", f"panel-{panel_id}")
    base_url = panel.get("base_url")

    username = panel.get("login") or panel.get("username")
    password = panel.get("password")

    inbound_id = panel.get("inbound_id") or 1

    res = {
        "panel_id": panel_id,
        "name": panel_name,
        "online": 0,
        "total": 0,
        "errors": [],
    }

    if not base_url or not username or not password:
        res["errors"].append("panel config incomplete (base_url/login/password)")
        return res

    try:
        api = Api(base_url, username, password)
        api.login()
    except Exception as e:
        msg = f"login failed (py3xui): {e}"
        res["errors"].append(msg)
        log_err(f"[{panel_name}] {msg}")
        return res

    try:
        inbound = api.inbound.get_by_id(inbound_id)
    except Exception as e:
        msg = f"cannot get inbound {inbound_id}: {e}"
        res["errors"].append(msg)
        log_err(f"[{panel_name}] {msg}")
        return res

    clients = getattr(inbound.settings, "clients", []) or []
    res["total"] = len(clients)

    sess = login_raw_session(base_url, username, password)
    if sess is None:
        res["errors"].append("cannot login via raw HTTP to get online count")
        return res

    online = get_online_count(base_url, sess, int(inbound_id))
    res["online"] = online

    return res


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
    if not isinstance(panels, list):
        print(json.dumps({"ok": False, "error": "panels must be list"}, ensure_ascii=False))
        return

    overall = {
        "ok": True,
        "panels": [],
    }

    for panel in panels:
        if not isinstance(panel, dict):
            continue
        if not panel.get("enabled", True):
            continue

        st = collect_panel_stats(panel)
        overall["panels"].append(st)
        if st["errors"]:
            overall["ok"] = False

    print(json.dumps(overall, ensure_ascii=False))


if __name__ == "__main__":
    main()
