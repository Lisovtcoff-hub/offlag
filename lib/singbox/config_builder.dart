// lib/singbox/config_builder.dart
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

import '../storage/services_store.dart';
import '../models/vpn_node.dart';

/// Маппинг: slug сервиса -> список доменных суффиксов,
/// которые нужно пустить в обход VPN (outbound: direct).
final Map<String, List<String>> kServiceDomainSuffixes = {
  'steam': [
    'steampowered.com',
    'steamcommunity.com',
    'steamstatic.com',
    'steamcontent.com',
    'steamgames.com',
  ],
  'epic-games': [
    'epicgames.com',
    'unrealengine.com',
  ],
  'youtube': [
    'youtube.com',
    'youtu.be',
    'ytimg.com',
    'googlevideo.com',
  ],
  'discord': [
    'discord.com',
    'discord.gg',
    'discordapp.com',
  ],
  'telegram': [
    'telegram.org',
    't.me',
  ],
  'twitch': [
    'twitch.tv',
    'ttvnw.net',
  ],
  'luma': [
    'luma.chat',
  ],
};

Future<void> generateSingboxConfig({
  required String configPath,
  required VpnNode node,
}) async {
  // 1) грузим исключённые сервисы
  final excluded = await ServicesStore.loadExcluded();
  debugPrint('[config] excluded services: $excluded');

  // 2) строим правила для сервисов
  final List<Map<String, dynamic>> serviceRules = [];
  for (final slug in excluded) {
    final domains = kServiceDomainSuffixes[slug];
    if (domains == null || domains.isEmpty) continue;

    serviceRules.add({
      'domain_suffix': domains,
      'outbound': 'direct',
    });
  }

  // 3) Параметры VLESS / Reality

  // serverHost — либо с бэка, либо из baseUrl (на всякий случай)
  final serverHost = node.serverHost.isNotEmpty
      ? node.serverHost
      : Uri.tryParse(node.baseUrl)?.host ?? '';

  final serverPort = node.serverPort != 0 ? node.serverPort : 443;

  // Для dev / страховки оставим фоллбеки,
  // чтобы конфиг не был пустым, если бэк ещё не настроен до конца.
  final uuid = node.uuid;
  final publicKey = node.publicKey;
  final shortId = node.shortId;

  if (serverHost.isEmpty || uuid.isEmpty || publicKey.isEmpty || shortId.isEmpty) {
    throw StateError('VLESS params missing for node id=${node.id}');
  }

  // 4) собираем полный конфиг
  final Map<String, dynamic> config = {
    'log': {
      'level': 'warning',
      'timestamp': true,
    },
    'dns': {
      'servers': [
        {
          'tag': 'local',
          'address': '1.1.1.1',
          'detour': 'direct',
        },
        {
          'tag': 'remote',
          'address': 'tls://8.8.8.8',
          'detour': 'vless-reality-out',
        },
      ],
      'rules': [
        {
          'domain_suffix': ['ru', 'xn--p1ai'],
          'server': 'local',
        },
        {
          'server': 'remote',
        },
      ],
      'final': 'remote',
      'strategy': 'ipv4_only',
    },
    'inbounds': [
      {
        'type': 'tun',
        'tag': 'tun-in',
        'interface_name': 'singtun0',
        'address': ['172.19.0.1/30'],
        'mtu': 1500,
        'auto_route': true,
        'strict_route': true,
        'stack': 'system',
        'sniff': true,
        'sniff_override_destination': true,
      },
    ],
    'outbounds': [
      {
        'type': 'vless',
        'tag': 'vless-reality-out',
        'server': serverHost,
        'server_port': serverPort,
        'uuid': uuid,
        'flow': 'xtls-rprx-vision',
        'tls': {
          'enabled': true,
          'server_name': 'google.com',
          'utls': {
            'enabled': true,
            'fingerprint': 'chrome',
          },
          'reality': {
            'enabled': true,
            'public_key': publicKey,
            'short_id': shortId,
          },
        },
        'transport': {
          'type': 'xhttp',
          'path': '/',
          'mode': 'auto',
          'host': '',
        },
      },
      {
        'type': 'direct',
        'tag': 'direct',
      },
      {
        'type': 'block',
        'tag': 'block',
      },
    ],
    'route': {
      'rules': [
        {
          'action': 'sniff',
        },
        {
          'protocol': 'dns',
          'action': 'hijack-dns',
        },
        {
          'ip_is_private': true,
          'outbound': 'direct',
        },
        {
          'domain_suffix': ['su', 'xn--p1ai'],
          'outbound': 'direct',
        },
        // правила для исключённых сервисов
        ...serviceRules,
      ],
      'default_domain_resolver': 'remote',
      'auto_detect_interface': true,
      'final': 'vless-reality-out',
    },
  };

  // 5) пишем файл
  final file = File(configPath);
  await file.writeAsString(
    const JsonEncoder.withIndent('  ').convert(config),
  );
  debugPrint('[config] sing-box config written to $configPath');
}
