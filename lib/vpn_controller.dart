import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_v2ray_plus/flutter_v2ray.dart';
import 'package:flutter_v2ray_plus/model/vless_status.dart';

import 'models/vpn_node.dart';
import 'windows/windows_vpn_manager.dart';

class VpnController {
  final WindowsVpnManager _windowsVpn = WindowsVpnManager();
  final FlutterV2ray _v2ray = FlutterV2ray();
  StreamSubscription<VlessStatus>? _v2raySub;
  bool _androidInitialized = false;
  static const List<String> _kAndroidDnsServers = <String>['1.1.1.1', '8.8.8.8'];
  String? _lastNodeKey;

  final ValueNotifier<bool> _status = ValueNotifier<bool>(false);
  ValueListenable<bool> get status => _status;
  bool get isRunning => _status.value;

  VpnController() {
    _windowsVpn.status.addListener(() {
      if (Platform.isWindows) {
        _status.value = _windowsVpn.isRunning;
      }
    });
  }

  static Future<void> killAllXray() => WindowsVpnManager.killAllXray();
  static Future<String> windowsLogPath() => WindowsVpnManager.getLogPath();

  Future<bool> connect(VpnNode node) async {
    if (Platform.isWindows) {
      return _windowsVpn.start(node);
    }
    if (Platform.isAndroid) {
      return _connectAndroid(node);
    }
    debugPrint('[VpnController] connect(): ${Platform.operatingSystem} not supported yet');
    return false;
  }

  Future<void> disconnect() async {
    if (Platform.isWindows) {
      await _windowsVpn.stop();
      return;
    }
    if (Platform.isAndroid) {
      await _disconnectAndroid();
      return;
    }
    debugPrint('[VpnController] disconnect(): ${Platform.operatingSystem} not supported yet');
  }

  Future<void> _ensureAndroidInitialized() async {
    if (!_androidInitialized) {
      await _v2ray.initializeVless();
      _androidInitialized = true;
    }
    _v2raySub ??= _v2ray.onStatusChanged.listen((status) {
      _status.value = status.state == 'CONNECTED';
    });
  }

  Future<bool> _connectAndroid(VpnNode node) async {
    final nodeKey = _nodeKey(node);
    if (_status.value && _lastNodeKey == nodeKey) return true;

    try {
      if (_status.value && _lastNodeKey != nodeKey) {
        await _disconnectAndroid();
      }
      await _ensureAndroidInitialized();
      final allowed = await _v2ray.requestPermission();
      if (!allowed) return false;

      final url = _buildVlessUrl(node);
      if (url.isEmpty) {
        debugPrint('[VpnController][Android] missing VLESS params for node id=${node.id}');
        return false;
      }
      final parsed = FlutterV2ray.parseFromURL(url);
      final config = _applyDnsOverrides(parsed.getFullConfiguration(), _kAndroidDnsServers);
      final remark = parsed.remark.isNotEmpty ? parsed.remark : node.name;

      await _v2ray.startVless(
        remark: remark,
        config: config,
        bypassSubnets: const <String>[],
        dnsServers: _kAndroidDnsServers,
      );

      _lastNodeKey = nodeKey;
      return true;
    } catch (e, st) {
      debugPrint('[VpnController][Android] failed to start: $e\n$st');
      return false;
    }
  }

  String _applyDnsOverrides(String config, List<String> dnsServers) {
    try {
      final decoded = jsonDecode(config);
      if (decoded is! Map<String, dynamic>) return config;
      decoded['dns'] = <String, dynamic>{
        'servers': dnsServers,
      };
      return jsonEncode(decoded);
    } catch (_) {
      return config;
    }
  }

  Future<void> _disconnectAndroid() async {
    try {
      await _v2ray.stopVless();
      await _v2raySub?.cancel();
      _v2raySub = null;
      _status.value = false;
    } catch (e, st) {
      debugPrint('[VpnController][Android] failed to stop: $e\n$st');
    }
  }

  String _nodeKey(VpnNode node) {
    return <String>[
      node.id.toString(),
      node.serverHost,
      node.serverPort.toString(),
      node.baseUrl,
      node.uuid,
      node.publicKey,
      node.shortId,
    ].join('|');
  }

  String _buildVlessUrl(VpnNode node) {
    final serverHost = node.serverHost.isNotEmpty ? node.serverHost : Uri.tryParse(node.baseUrl)?.host ?? '';
    final serverPort = node.serverPort != 0 ? node.serverPort : 443;
    final uuid = node.uuid;
    final publicKey = node.publicKey;
    final shortId = node.shortId;
    final remark = Uri.encodeComponent(node.name);

    if (serverHost.isEmpty || uuid.isEmpty || publicKey.isEmpty || shortId.isEmpty) {
      return '';
    }

    return 'vless://$uuid@$serverHost:$serverPort?'
        'type=xhttp&encryption=none&security=reality&pbk=$publicKey'
        '&fp=chrome&sni=web.max.ru&sid=$shortId&spx=%2F#$remark';
  }
}
