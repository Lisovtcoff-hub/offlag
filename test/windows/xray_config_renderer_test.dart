import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:offlag/windows/xray_config_renderer.dart';

void main() {
  const template = r'''
{
  // line comment
  "outbounds": [
    {
      "tag": "proxy",
      "settings": {
        "vnext": [
          {
            "address": {{SERVER_ADDR}},
            "port": {{SERVER_PORT}},
            "users": [{ "id": {{VLESS_ID}} }]
          }
        ]
      },
      "streamSettings": {
        "realitySettings": {
          "serverName": {{SNI}},
          "fingerprint": {{FP}},
          "publicKey": {{PBK}},
          "shortId": {{SID}},
          "spiderX": {{SPX}}
        },
        "xhttpSettings": {
          "path": {{XHTTP_PATH}},
          "mode": {{XHTTP_MODE}}
        }
      }
    },
    {
      "tag": "direct-lan",
      "sendThrough": {{SEND_THROUGH_LAN_IP}}
    }
  ]
}
''';

  test('renderXrayConfig replaces placeholders and returns valid JSON', () {
    const profile = XrayRenderProfile(
      vlessId: 'id-123',
      serverAddr: '77.221.145.78',
      serverPort: 443,
      sni: 'web.max.ru',
      fp: 'chrome',
      pbk: 'pub-key',
      sid: '26',
      spx: '/',
      xhttpPath: '/',
      xhttpMode: 'auto',
    );

    final rendered = renderXrayConfig(template, profile, '192.168.31.232');
    final decoded = jsonDecode(rendered) as Map<String, dynamic>;
    final outbounds = decoded['outbounds'] as List<dynamic>;
    final proxy = outbounds.first as Map<String, dynamic>;
    final directLan = outbounds[1] as Map<String, dynamic>;

    final settings = proxy['settings'] as Map<String, dynamic>;
    final vnext = settings['vnext'] as List<dynamic>;
    final first = vnext.first as Map<String, dynamic>;
    final users = first['users'] as List<dynamic>;
    final user = users.first as Map<String, dynamic>;
    final stream = proxy['streamSettings'] as Map<String, dynamic>;
    final reality = stream['realitySettings'] as Map<String, dynamic>;

    expect(first['address'], '77.221.145.78');
    expect(first['port'], 443);
    expect(user['id'], 'id-123');
    expect(reality['publicKey'], 'pub-key');
    expect(reality['shortId'], '26');
    expect(directLan['sendThrough'], '192.168.31.232');
  });

  test('renderXrayConfig escapes string values', () {
    const profile = XrayRenderProfile(
      vlessId: 'id"\\value',
      serverAddr: 'example.com',
      serverPort: 443,
      sni: 'sni"value',
      fp: 'chrome',
      pbk: 'pk"\\',
      sid: '26',
      spx: '/a"b',
      xhttpPath: '/a"b',
      xhttpMode: 'auto',
    );

    final rendered = renderXrayConfig(template, profile, '10.0.0.7');
    final decoded = jsonDecode(rendered) as Map<String, dynamic>;
    final outbounds = decoded['outbounds'] as List<dynamic>;
    final proxy = outbounds.first as Map<String, dynamic>;
    final settings = proxy['settings'] as Map<String, dynamic>;
    final vnext = settings['vnext'] as List<dynamic>;
    final first = vnext.first as Map<String, dynamic>;
    final users = first['users'] as List<dynamic>;
    final user = users.first as Map<String, dynamic>;

    expect(user['id'], 'id"\\value');
  });

  test('renderXrayConfig throws when placeholder is unresolved', () {
    const badTemplate = '{"a": {{UNKNOWN}} }';
    const profile = XrayRenderProfile(
      vlessId: 'id',
      serverAddr: 'example.com',
      serverPort: 443,
      sni: 'sni',
      fp: 'chrome',
      pbk: 'pbk',
      sid: 'sid',
      spx: '/',
      xhttpPath: '/',
      xhttpMode: 'auto',
    );

    expect(
      () => renderXrayConfig(badTemplate, profile, '192.168.0.2'),
      throwsA(isA<FormatException>()),
    );
  });
}
