import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart' show rootBundle;
import 'package:path_provider/path_provider.dart';

import '../models/vpn_node.dart';
import 'xray_config_renderer.dart';

enum WindowsVpnStartError {
  none,
  requiresAdmin,
  unknown,
}

class WindowsVpnManager {
  static const String _tunName = 'xray0';
  static const String _tunAddress = '198.18.0.1';
  static const String _tunMask = '255.254.0.0';
  static const String _fallbackServerIp = '77.221.145.78';
  static const String _cfgAssetPath = 'assets/vpn/client-tun.template.jsonc';
  static const List<String> _runtimeFiles = <String>[
    'xray.exe',
    'wintun.dll',
    'geoip.dat',
    'geosite.dat',
  ];

  final ValueNotifier<bool> _status = ValueNotifier<bool>(false);
  ValueListenable<bool> get status => _status;
  bool get isRunning => _status.value;
  WindowsVpnStartError _lastStartError = WindowsVpnStartError.none;
  WindowsVpnStartError get lastStartError => _lastStartError;

  Process? _proc;
  File? _logFile;
  String? _activeServerIp;
  int? _tunIfIndex;
  bool _defaultRouteApplied = false;
  bool _serverBypassApplied = false;
  final List<String> _dnsBypassIps = <String>[];
  bool _tunIpApplied = false;

  static Future<String> getLogPath() async {
    final appSupport = await getApplicationSupportDirectory();
    final logsDir = _joinPath(appSupport.path, 'logs');
    return _joinPath(logsDir, 'windows-vpn.log');
  }

  Future<bool> start(VpnNode node) async {
    if (!Platform.isWindows) return false;
    if (_proc != null) return true;
    _lastStartError = WindowsVpnStartError.none;

    final ops = <Future<void> Function()>[];
    try {
      await _prepareLogging();
      await _log('=== START WINDOWS VPN ===');
      final elevated = await _isElevated();
      await _log('Elevation check: isAdmin=$elevated');
      if (!elevated) {
        _lastStartError = WindowsVpnStartError.requiresAdmin;
        await _log('Start aborted: administrator rights required');
        return false;
      }

      final runtimeDir = await _prepareRuntimeDir();

      final routeInfo = await _getDefaultRouteInfo();
      if (routeInfo == null) {
        await _log('No default IPv4 route found');
        return false;
      }
      await _log('Default route: gateway=${routeInfo.gateway}, ifIndex=${routeInfo.ifIndex}, lanIp=${routeInfo.lanIp}');

      _dnsBypassIps.clear();
      final dnsServers = await _getDnsServers(routeInfo.ifIndex);
      await _log('Physical interface DNS servers: ${dnsServers.join(', ')}');
      for (final dns in dnsServers) {
        if (!_isValidDnsBypassTarget(dns)) {
          await _log('Skip invalid DNS bypass target: $dns');
          continue;
        }
        await _runCmd(
          'route',
          <String>['delete', dns],
          failOnError: false,
        );
        final dnsBypassRes = await _runCmd(
          'route',
          <String>[
            'add',
            dns,
            'mask',
            '255.255.255.255',
            routeInfo.gateway,
            'if',
            routeInfo.ifIndex.toString(),
            'metric',
            '1',
          ],
          failOnError: false,
        );
        if (dnsBypassRes.exitCode == 0) {
          _dnsBypassIps.add(dns);
        } else {
          await _log('Failed to add DNS bypass route for $dns, continue without it');
        }
      }
      ops.add(() async => _deleteDnsBypassRoutes());

      final serverIp = await _resolveServerIp(node);
      _activeServerIp = serverIp;
      await _log('Server IP for anti-loop route: $serverIp');

      final cfgPath = await _generateConfig(
        node: node,
        lanIp: routeInfo.lanIp,
        runtimeDir: runtimeDir,
      );
      await _log('Generated config: $cfgPath');

      final proc = await Process.start(
        _joinPath(runtimeDir.path, 'xray.exe'),
        <String>['run', '-c', cfgPath],
        workingDirectory: runtimeDir.path,
        runInShell: false,
        mode: ProcessStartMode.normal,
      );
      _proc = proc;
      await _log('xray started pid=${proc.pid}');

      proc.stdout.transform(utf8.decoder).listen((data) {
        unawaited(_log('[xray][out] ${data.trimRight()}'));
      });
      proc.stderr.transform(utf8.decoder).listen((data) {
        unawaited(_log('[xray][err] ${data.trimRight()}'));
      });
      proc.exitCode.then((code) async {
        await _log('xray exited code=$code');
        _proc = null;
        _status.value = false;
      });

      final addServerBypassRes = await _runCmd(
        'route',
        <String>[
          'add',
          serverIp,
          'mask',
          '255.255.255.255',
          routeInfo.gateway,
          'if',
          routeInfo.ifIndex.toString(),
          'metric',
          '1',
        ],
      );
      if (addServerBypassRes.exitCode != 0) {
        throw StateError('Failed to add server anti-loop route');
      }
      _serverBypassApplied = true;
      ops.add(() async => _deleteServerBypassRoute());

      final tunIfIndex = await _waitForTunAdapterIndex();
      if (tunIfIndex == null) {
        throw StateError('Failed to resolve TUN adapter ifIndex');
      }
      _tunIfIndex = tunIfIndex;
      await _log('TUN ifIndex=$tunIfIndex');

      final setTunIpRes = await _runCmd(
        'netsh',
        <String>[
          'interface',
          'ipv4',
          'set',
          'address',
          'name=$_tunName',
          'static',
          _tunAddress,
          _tunMask,
        ],
      );
      if (setTunIpRes.exitCode != 0) {
        throw StateError('Failed to set TUN IPv4 address');
      }
      _tunIpApplied = true;
      ops.add(() async => _setTunDhcp());

      final addDefaultRouteRes = await _runCmd(
        'route',
        <String>[
          'add',
          '0.0.0.0',
          'mask',
          '0.0.0.0',
          _tunAddress,
          'if',
          tunIfIndex.toString(),
          'metric',
          '10',
        ],
      );
      if (addDefaultRouteRes.exitCode != 0) {
        throw StateError('Failed to add default route via xray0');
      }
      _defaultRouteApplied = true;
      ops.add(() async => _deleteDefaultRoute());

      final p1 = await _runCmd(
        'netsh',
        <String>['interface', 'ipv6', 'set', 'prefix', '::/96', '60', '3'],
      );
      if (p1.exitCode != 0) {
        throw StateError('Failed to apply IPv6 prefix policy ::/96');
      }

      final p2 = await _runCmd(
        'netsh',
        <String>[
          'interface',
          'ipv6',
          'set',
          'prefix',
          '::ffff:0:0/96',
          '55',
          '4'
        ],
      );
      if (p2.exitCode != 0) {
        throw StateError('Failed to apply IPv6 prefix policy ::ffff:0:0/96');
      }

      final checkOk = await _runPostStartChecks(tunIfIndex);
      if (!checkOk) {
        throw StateError('Post-start health check failed');
      }

      _status.value = true;
      await _log('VPN status=ON');
      return true;
    } catch (e, st) {
      _lastStartError = WindowsVpnStartError.unknown;
      await _log('Start failed: $e\n$st');
      await _rollback(ops.reversed.toList());
      await stop();
      return false;
    }
  }

  Future<void> stop() async {
    if (!Platform.isWindows) return;
    await _prepareLogging();
    await _log('=== STOP WINDOWS VPN ===');

    await _deleteDefaultRoute();
    await _deleteDnsBypassRoutes();
    await _deleteServerBypassRoute();
    await _setTunDhcp();

    final proc = _proc;
    if (proc != null) {
      try {
        proc.kill();
        await _log('xray process kill signal sent');
      } catch (e) {
        await _log('xray process kill failed: $e');
      }
      _proc = null;
    }

    // If process still remains or was not tracked, force-kill by image name.
    await _runCmd(
      'taskkill',
      <String>['/IM', 'xray.exe', '/F'],
      failOnError: false,
    );

    _status.value = false;
    _activeServerIp = null;
    _tunIfIndex = null;
    _defaultRouteApplied = false;
    _serverBypassApplied = false;
    _dnsBypassIps.clear();
    _tunIpApplied = false;
    await _log('VPN status=OFF');
  }

  static Future<void> killAllXray() async {
    if (!Platform.isWindows) return;
    try {
      await Process.run(
        'taskkill',
        <String>['/IM', 'xray.exe', '/F'],
        runInShell: true,
      );
    } catch (_) {}
  }

  static Future<bool> relaunchAsAdmin() async {
    if (!Platform.isWindows) return false;
    final exe = Platform.resolvedExecutable.replaceAll("'", "''");
    final res = await Process.run(
      'powershell',
      <String>[
        '-NoProfile',
        '-Command',
        "Start-Process -FilePath '$exe' -Verb RunAs",
      ],
      runInShell: true,
    );
    return res.exitCode == 0;
  }

  Future<void> _rollback(List<Future<void> Function()> ops) async {
    for (final op in ops) {
      try {
        await op();
      } catch (e) {
        await _log('Rollback step failed: $e');
      }
    }
  }

  Future<void> _prepareLogging() async {
    if (_logFile != null) return;
    final logPath = await getLogPath();
    final logDir = Directory(File(logPath).parent.path);
    if (!logDir.existsSync()) {
      logDir.createSync(recursive: true);
    }
    _logFile = File(logPath);
  }

  Future<void> _log(String message) async {
    final file = _logFile;
    if (file == null) return;
    final ts = DateTime.now().toIso8601String();
    await file.writeAsString('[$ts] $message\n', mode: FileMode.append, flush: true);
  }

  Future<Directory> _prepareRuntimeDir() async {
    final appSupport = await getApplicationSupportDirectory();
    final runtimeDir = Directory(_joinPath(appSupport.path, 'xray-runtime'));
    if (!runtimeDir.existsSync()) {
      runtimeDir.createSync(recursive: true);
    }

    final sourceDir = await _findRuntimeSourceDir();
    if (sourceDir == null) {
      throw StateError('Cannot find Xray runtime source files');
    }

    for (final name in _runtimeFiles) {
      final src = File(_joinPath(sourceDir.path, name));
      final dstPath = _joinPath(runtimeDir.path, name);
      final dst = File(dstPath);
      if (!src.existsSync()) {
        throw StateError('Missing runtime file: ${src.path}');
      }
      await src.copy(dstPath);
      await _log('Runtime file copied: ${src.path} -> ${dst.path}');
    }
    return runtimeDir;
  }

  Future<Directory?> _findRuntimeSourceDir() async {
    final candidates = <Directory>[];
    try {
      final exeDir = File(Platform.resolvedExecutable).parent;
      candidates.add(exeDir);
      candidates.add(Directory(_joinPath(exeDir.path, 'third_party\\xray\\windows-amd64')));
      candidates.add(Directory(_joinPath(exeDir.path, '..\\..\\..\\third_party\\xray\\windows-amd64')));
    } catch (_) {}
    candidates.add(Directory(_joinPath(Directory.current.path, 'third_party\\xray\\windows-amd64')));

    for (final dir in candidates) {
      if (!dir.existsSync()) continue;
      final hasAll = _runtimeFiles.every((name) => File(_joinPath(dir.path, name)).existsSync());
      if (hasAll) {
        await _log('Runtime source selected: ${dir.path}');
        return dir;
      }
    }
    return null;
  }

  Future<String> _generateConfig({
    required VpnNode node,
    required String lanIp,
    required Directory runtimeDir,
  }) async {
    final template = await rootBundle.loadString(_cfgAssetPath);
    final serverHost = node.serverHost.isNotEmpty ? node.serverHost : _fallbackServerIp;
    final serverPort = node.serverPort > 0 ? node.serverPort : 443;
    if (node.uuid.isEmpty || node.publicKey.isEmpty || node.shortId.isEmpty) {
      throw StateError('Node params missing: uuid/publicKey/shortId');
    }

    final profile = XrayRenderProfile(
      vlessId: node.uuid,
      serverAddr: serverHost,
      serverPort: serverPort,
      sni: 'web.max.ru',
      fp: 'chrome',
      pbk: node.publicKey,
      sid: node.shortId,
      spx: '/',
      xhttpPath: '/',
      xhttpMode: 'auto',
    );
    final finalConfig = renderXrayConfig(template, profile, lanIp);
    final cfgPath = _joinPath(runtimeDir.path, 'client-tun.jsonc');
    final file = File(cfgPath);
    await file.writeAsString(finalConfig, flush: true);
    return cfgPath;
  }

  Future<_RouteInfo?> _getDefaultRouteInfo() async {
    final res = await _runPowerShell(
      r"$rt = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -AddressFamily IPv4 | "
      r"Sort-Object -Property RouteMetric | Select-Object -First 1 InterfaceIndex,NextHop; "
      r"if ($null -eq $rt) { exit 3 } ;"
      r"$ip = Get-NetIPAddress -InterfaceIndex $rt.InterfaceIndex -AddressFamily IPv4 | "
      r"Where-Object { $_.IPAddress -ne '127.0.0.1' -and $_.IPAddress -notlike '169.254.*' } | "
      r"Select-Object -First 1 -ExpandProperty IPAddress; "
      r"if ([string]::IsNullOrWhiteSpace($ip)) { exit 4 } ;"
      r"@{ ifIndex = $rt.InterfaceIndex; gateway = $rt.NextHop; lanIp = $ip } | ConvertTo-Json -Compress",
      failOnError: false,
    );
    if (res.exitCode != 0) return null;
    final out = (res.stdout ?? '').toString().trim();
    if (out.isEmpty) return null;

    final data = jsonDecode(out);
    if (data is! Map<String, dynamic>) return null;
    final ifIndex = data['ifIndex'] as num?;
    final gateway = (data['gateway'] ?? '').toString().trim();
    final lanIp = (data['lanIp'] ?? '').toString().trim();
    if (ifIndex == null || gateway.isEmpty || lanIp.isEmpty) return null;
    return _RouteInfo(ifIndex.toInt(), gateway, lanIp);
  }

  Future<int?> _getTunInterfaceIndex() async {
    for (int i = 0; i < 40; i++) {
      final res = await _runPowerShell(
        "(Get-NetIPInterface -InterfaceAlias '$_tunName' -AddressFamily IPv4 | Select-Object -First 1 -ExpandProperty InterfaceIndex)",
        failOnError: false,
      );
      if (res.exitCode == 0) {
        final out = (res.stdout ?? '').toString().trim();
        final idx = int.tryParse(out);
        if (idx != null) return idx;
      }
      await Future.delayed(const Duration(milliseconds: 250));
    }
    return null;
  }

  Future<int?> _waitForTunAdapterIndex() async {
    for (int i = 0; i < 40; i++) {
      final res = await _runPowerShell(
        "(Get-NetAdapter -Name '$_tunName' -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty ifIndex)",
        failOnError: false,
      );
      if (res.exitCode == 0) {
        final out = (res.stdout ?? '').toString().trim();
        final idx = int.tryParse(out);
        if (idx != null) return idx;
      }
      await Future.delayed(const Duration(milliseconds: 250));
    }
    return null;
  }

  Future<String> _resolveServerIp(VpnNode node) async {
    final host = node.serverHost.trim();
    if (_isIpv4(host)) return host;
    if (host.isNotEmpty) {
      try {
        final list = await InternetAddress.lookup(host, type: InternetAddressType.IPv4);
        for (final addr in list) {
          if (addr.type == InternetAddressType.IPv4) {
            return addr.address;
          }
        }
      } catch (_) {}
    }
    return _fallbackServerIp;
  }

  Future<List<String>> _getDnsServers(int ifIndex) async {
    final res = await _runPowerShell(
      "\$v = Get-DnsClientServerAddress -InterfaceIndex $ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | "
      "Select-Object -First 1 -ExpandProperty ServerAddresses; "
      "if (\$null -eq \$v) { '[]' } else { \$v | ConvertTo-Json -Compress }",
      failOnError: false,
    );
    if (res.exitCode != 0) return <String>[];
    final raw = (res.stdout ?? '').toString().trim();
    if (raw.isEmpty) return <String>[];
    try {
      final decoded = jsonDecode(raw);
      if (decoded is List) {
        return decoded
            .map((e) => e.toString().trim())
            .where(_isValidDnsBypassTarget)
            .toSet()
            .toList(growable: false);
      }
      final single = decoded.toString().trim();
      if (_isValidDnsBypassTarget(single)) return <String>[single];
    } catch (_) {
      final lines = raw
          .split(RegExp(r'[\r\n]+'))
          .map((e) => e.trim())
          .where(_isValidDnsBypassTarget)
          .toSet()
          .toList(growable: false);
      return lines;
    }
    return <String>[];
  }

  Future<bool> _runPostStartChecks(int tunIfIndex) async {
    final ready = await _waitForTunOrProcessHealthy(tunIfIndex);
    if (!ready) {
      await _log('Health check: TUN/process readiness failed');
      return false;
    }

    final ns = await _runCmd(
      'nslookup',
      <String>['chatgpt.com'],
      failOnError: false,
    );
    final nsOut = (ns.stdout ?? '').toString();
    final nsOk = ns.exitCode == 0 && _containsIpv4(nsOut);
    await _log('Health check nslookup ok=$nsOk');

    final httpIfconfig = await _runHttpProbe('https://ifconfig.me/ip');
    final http2ip = await _runHttpProbe('https://2ip.io/ip');
    final httpChatgpt = await _runHttpsStatusProbe('https://chatgpt.com');
    final httpOk = httpIfconfig || http2ip || httpChatgpt;
    final ipHttpsOk = await _runIpHttpsProbe();
    await _log(
      'Health check http ok=$httpOk ifconfig=$httpIfconfig 2ip=$http2ip chatgpt=$httpChatgpt ipHttps=$ipHttpsOk',
    );

    // На части Windows-машин системный стек curl/WinHTTP может падать по DNS,
    // при этом реальный трафик через TUN работает. Не валим старт только из-за
    // этих probe: считаем успехом, если прошел хотя бы один канал проверки.
    final finalOk = nsOk || httpOk || ipHttpsOk;
    await _log('Health check final ok=$finalOk');
    return finalOk;
  }

  Future<bool> _runHttpProbe(String url) async {
    final curl = await _runCmd(
      'curl',
      <String>['-4', '-sS', '--max-time', '12', url],
      failOnError: false,
    );
    final curlOut = (curl.stdout ?? '').toString().trim();
    final curlOk = curl.exitCode == 0 && _containsIpv4(curlOut);
    await _log('Health check curl url=$url ok=$curlOk body="$curlOut"');
    if (curlOk) return true;

    final ps = await _runPowerShell(
      "(Invoke-WebRequest -UseBasicParsing -Uri '$url' -TimeoutSec 12).Content",
      failOnError: false,
    );
    final psOut = (ps.stdout ?? '').toString().trim();
    final psOk = ps.exitCode == 0 && _containsIpv4(psOut);
    await _log('Health check powershell url=$url ok=$psOk body="$psOut"');
    return psOk;
  }

  Future<bool> _runHttpsStatusProbe(String url) async {
    final curl = await _runCmd(
      'curl',
      <String>[
        '-4',
        '-sS',
        '-o',
        'NUL',
        '-w',
        '%{http_code}',
        '--max-time',
        '12',
        url,
      ],
      failOnError: false,
    );
    final curlOut = (curl.stdout ?? '').toString().trim();
    final code = int.tryParse(curlOut);
    final curlOk = curl.exitCode == 0 && code != null && code >= 200 && code < 500;
    await _log('Health check curl-status url=$url ok=$curlOk code="$curlOut"');
    if (curlOk) return true;

    final ps = await _runPowerShell(
      "(Invoke-WebRequest -UseBasicParsing -Uri '$url' -TimeoutSec 12).StatusCode",
      failOnError: false,
    );
    final psOut = (ps.stdout ?? '').toString().trim();
    final psCode = int.tryParse(psOut);
    final psOk = ps.exitCode == 0 && psCode != null && psCode >= 200 && psCode < 500;
    await _log('Health check powershell-status url=$url ok=$psOk code="$psOut"');
    return psOk;
  }

  Future<bool> _runIpHttpsProbe() async {
    const ips = <String>['1.1.1.1', '8.8.8.8'];
    for (final ip in ips) {
      final curl = await _runCmd(
        'curl',
        <String>[
          '-4',
          '-k',
          '-sS',
          '-o',
          'NUL',
          '-w',
          '%{http_code}',
          '--max-time',
          '12',
          'https://$ip',
        ],
        failOnError: false,
      );
      final curlOut = (curl.stdout ?? '').toString().trim();
      final code = int.tryParse(curlOut);
      final ok = curl.exitCode == 0 && code != null && code > 0;
      await _log('Health check ip-https ip=$ip ok=$ok code="$curlOut"');
      if (ok) return true;
    }
    return false;
  }

  Future<bool> _waitForTunOrProcessHealthy(int expectedTunIfIndex) async {
    for (int i = 0; i < 20; i++) {
      final proc = _proc;
      if (proc == null) return false;

      final tunIdx = await _getTunInterfaceIndex();
      if (tunIdx != null && tunIdx == expectedTunIfIndex) {
        return true;
      }

      await Future.delayed(const Duration(milliseconds: 300));
    }
    return false;
  }

  Future<void> _deleteDefaultRoute() async {
    if (!_defaultRouteApplied) return;
    final idx = _tunIfIndex;
    if (idx == null) return;
    await _runCmd(
      'route',
      <String>[
        'delete',
        '0.0.0.0',
        'mask',
        '0.0.0.0',
        _tunAddress,
        'if',
        idx.toString(),
      ],
      failOnError: false,
    );
    _defaultRouteApplied = false;
  }

  Future<void> _deleteServerBypassRoute() async {
    if (!_serverBypassApplied) return;
    final serverIp = _activeServerIp ?? _fallbackServerIp;
    await _runCmd(
      'route',
      <String>['delete', serverIp],
      failOnError: false,
    );
    _serverBypassApplied = false;
  }

  Future<void> _deleteDnsBypassRoutes() async {
    if (_dnsBypassIps.isEmpty) return;
    for (final ip in _dnsBypassIps.toList()) {
      if (!_isValidDnsBypassTarget(ip)) continue;
      await _runCmd(
        'route',
        <String>['delete', ip],
        failOnError: false,
      );
    }
    _dnsBypassIps.clear();
  }

  Future<void> _setTunDhcp() async {
    if (!_tunIpApplied) return;
    await _runCmd(
      'netsh',
      <String>[
        'interface',
        'ipv4',
        'set',
        'address',
        'name=$_tunName',
        'dhcp',
      ],
      failOnError: false,
    );
    _tunIpApplied = false;
  }

  Future<bool> _isElevated() async {
    final res = await _runPowerShell(
      r"([Security.Principal.WindowsPrincipal]"
      r"[Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole("
      r"[Security.Principal.WindowsBuiltInRole]::Administrator)",
      failOnError: false,
    );
    if (res.exitCode != 0) return false;
    final out = (res.stdout ?? '').toString().trim().toLowerCase();
    return out == 'true';
  }

  Future<ProcessResult> _runPowerShell(
    String script, {
    bool failOnError = true,
  }) {
    return _runCmd(
      'powershell',
      <String>['-NoProfile', '-Command', script],
      failOnError: failOnError,
    );
  }

  Future<ProcessResult> _runCmd(
    String executable,
    List<String> arguments, {
    bool failOnError = true,
  }) async {
    final cmd = '$executable ${arguments.join(' ')}';
    await _log('CMD> $cmd');
    final res = await Process.run(
      executable,
      arguments,
      runInShell: true,
    );
    final stdout = (res.stdout ?? '').toString().trimRight();
    final stderr = (res.stderr ?? '').toString().trimRight();
    await _log('EXIT=${res.exitCode}');
    if (stdout.isNotEmpty) await _log('STDOUT:\n$stdout');
    if (stderr.isNotEmpty) await _log('STDERR:\n$stderr');
    if (failOnError && res.exitCode != 0) {
      throw ProcessException(executable, arguments, stderr, res.exitCode);
    }
    return res;
  }

  static bool _isIpv4(String input) {
    final parts = input.split('.');
    if (parts.length != 4) return false;
    for (final p in parts) {
      final n = int.tryParse(p);
      if (n == null || n < 0 || n > 255) return false;
    }
    return true;
  }

  static bool _containsIpv4(String text) {
    final match = RegExp(
      r'\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b',
    ).firstMatch(text);
    return match != null;
  }

  static bool _isValidDnsBypassTarget(String ip) {
    if (!_isIpv4(ip)) return false;
    if (ip == '0.0.0.0' || ip == '255.255.255.255') return false;
    if (ip.startsWith('127.') || ip.startsWith('169.254.')) return false;
    return true;
  }

  static String _joinPath(String dir, String name) {
    final sep = Platform.pathSeparator;
    if (dir.endsWith(sep)) return '$dir$name';
    return '$dir$sep$name';
  }
}

class _RouteInfo {
  final int ifIndex;
  final String gateway;
  final String lanIp;

  const _RouteInfo(this.ifIndex, this.gateway, this.lanIp);
}
