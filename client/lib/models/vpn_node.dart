// lib/models/vpn_node.dart

class VpnNode {
  final int id;
  final String name;
  final String country;
  final String baseUrl;

  /// Статистика нагрузки с бэка
  final int online;
  final int total;

  /// Приоритет панели (меньше = важнее)
  final int priority;

  /// VLESS / Reality параметры для этой ноды
  ///
  /// serverHost / serverPort — куда подключаемся (host/IP и порт)
  /// uuid — UUID конкретного клиента (юзера) на панели
  /// publicKey / shortId — параметры Reality inbound'а
  final String serverHost;
  final int serverPort;
  final String uuid;
  final String publicKey;
  final String shortId;
  final bool premium;

  /// Пинг, который меряем на клиенте
  int? pingMs;

  VpnNode({
    required this.id,
    required this.name,
    required this.country,
    required this.baseUrl,
    required this.online,
    required this.total,
    required this.priority,
    required this.serverHost,
    required this.serverPort,
    required this.uuid,
    required this.publicKey,
    required this.shortId,
    required this.premium,
    this.pingMs,
  });

  factory VpnNode.fromJson(Map<String, dynamic> json) {
    return VpnNode(
      id: json['id'] as int,
      name: json['name'] as String,
      country: json['country'] as String,
      baseUrl: json['base_url'] as String,
      online: json['online'] as int? ?? 0,
      total: json['total'] as int? ?? 0,
      priority: json['priority'] as int? ?? 100,
      serverHost: json['server_host'] as String? ?? '',
      serverPort: json['server_port'] as int? ?? 443,
      uuid: json['uuid'] as String? ?? '',
      publicKey: json['public_key'] as String? ?? '',
      shortId: json['short_id'] as String? ?? '',
      premium: json['premium'] == true,
    );
  }
}
