import 'dart:convert';

class XrayRenderProfile {
  final String vlessId;
  final String serverAddr;
  final int serverPort;
  final String sni;
  final String fp;
  final String pbk;
  final String sid;
  final String spx;
  final String xhttpPath;
  final String xhttpMode;

  const XrayRenderProfile({
    required this.vlessId,
    required this.serverAddr,
    required this.serverPort,
    required this.sni,
    required this.fp,
    required this.pbk,
    required this.sid,
    required this.spx,
    required this.xhttpPath,
    required this.xhttpMode,
  });
}

String renderXrayConfig(
  String template,
  XrayRenderProfile profile,
  String lanIp,
) {
  final replacements = <String, String>{
    '{{VLESS_ID}}': jsonEncode(profile.vlessId),
    '{{SERVER_ADDR}}': jsonEncode(profile.serverAddr),
    '{{SERVER_PORT}}': profile.serverPort.toString(),
    '{{SNI}}': jsonEncode(profile.sni),
    '{{FP}}': jsonEncode(profile.fp),
    '{{PBK}}': jsonEncode(profile.pbk),
    '{{SID}}': jsonEncode(profile.sid),
    '{{SPX}}': jsonEncode(profile.spx),
    '{{XHTTP_PATH}}': jsonEncode(profile.xhttpPath),
    '{{XHTTP_MODE}}': jsonEncode(profile.xhttpMode),
    '{{SEND_THROUGH_LAN_IP}}': jsonEncode(lanIp),
  };

  var rendered = template;
  replacements.forEach((token, value) {
    rendered = rendered.replaceAll(token, value);
  });

  final unresolved = RegExp(r'\{\{[A-Z0-9_]+\}\}');
  if (unresolved.hasMatch(rendered)) {
    throw const FormatException('Template has unresolved placeholders');
  }

  final jsonNoComments = _stripJsonComments(rendered);
  final decoded = jsonDecode(jsonNoComments);
  return const JsonEncoder.withIndent('  ').convert(decoded);
}

String _stripJsonComments(String input) {
  final out = StringBuffer();
  var i = 0;
  var inString = false;
  var escaped = false;
  while (i < input.length) {
    final ch = input[i];
    final next = i + 1 < input.length ? input[i + 1] : '';

    if (inString) {
      out.write(ch);
      if (escaped) {
        escaped = false;
      } else if (ch == r'\') {
        escaped = true;
      } else if (ch == '"') {
        inString = false;
      }
      i++;
      continue;
    }

    if (ch == '"') {
      inString = true;
      out.write(ch);
      i++;
      continue;
    }

    if (ch == '/' && next == '/') {
      i += 2;
      while (i < input.length && input[i] != '\n') {
        i++;
      }
      continue;
    }

    if (ch == '/' && next == '*') {
      i += 2;
      while (i + 1 < input.length && !(input[i] == '*' && input[i + 1] == '/')) {
        i++;
      }
      i = (i + 1 < input.length) ? i + 2 : input.length;
      continue;
    }

    out.write(ch);
    i++;
  }
  return out.toString();
}
