import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../theme.dart';
import '../net.dart';
import '../models/vpn_node.dart';

/// Экран выбора сервера.
///
/// Берёт реальные ноды с бэка (/vpn/nodes).
/// При открытии:
///  - грузит список серверов;
///  - для каждого меряет TCP-пинг;
///  - считает загрузку как online * 100 / 40;
/// По нажатию "Выбрать" возвращает выбранную ноду через Navigator.pop.
class ServersPage extends StatefulWidget {
  final bool includeAll;
  const ServersPage({super.key, this.includeAll = false});

  @override
  State<ServersPage> createState() => _ServersPageState();
}

class _ServersPageState extends State<ServersPage> {
  bool _loading = true;
  String? _error;
  List<VpnNode> _nodes = [];

  @override
  void initState() {
    super.initState();
    _loadNodes();
  }

  Future<void> _loadNodes() async {
    setState(() {
      _loading = true;
      _error = null;
      _nodes = [];
    });

    try {
      // 1) забираем список нод с бэка
      final nodes = await fetchVpnNodes(includeAll: widget.includeAll);

      // 2) для каждой ноды меряем пинг
      for (final node in nodes) {
        final hp = hostPortFromNode(node);
        if (hp == null) continue;
        final ping = await measureTcpPing(hp.host, hp.port);
        if (ping != null) {
          node.pingMs = ping;
        }
      }

      if (!mounted) return;
      setState(() {
        _nodes = nodes;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Не удалось загрузить список серверов. Попробуйте ещё раз.';
      });
    } finally {
      if (!mounted) return;
      setState(() {
        _loading = false;
      });
    }
  }

  /// Преобразуем двухбуквенный код страны в emoji-флаг (NL -> 🇳🇱).
  String _flagEmoji(String countryCode) {
    final code = countryCode.trim().toUpperCase();
    if (code.length != 2) return '🌐';
    final first = code.codeUnitAt(0) - 0x41 + 0x1F1E6;
    final second = code.codeUnitAt(1) - 0x41 + 0x1F1E6;
    return String.fromCharCodes([first, second]);
  }

  /// Путь к svg-иконке флага из assets/icons.
  String _flagAssetPath(String countryCode) {
    final code = countryCode.trim().toLowerCase();
    // 👇 если у тебя другая схема имён — поправь тут
    return 'assets/icons/$code.svg';
  }

  /// Виджет флага: сначала пробуем svg-иконку из assets,
  /// если ассета нет/не загрузился — показываем emoji.
  Widget _buildFlag(String countryCode) {
    final emoji = _flagEmoji(countryCode);
    if (countryCode.trim().length != 2) {
      return Text(
        emoji,
        style: const TextStyle(fontSize: 22),
      );
    }

    return SvgPicture.asset(
      _flagAssetPath(countryCode),
      width: 26,
      height: 26,
      fit: BoxFit.contain,
      placeholderBuilder: (_) => Text(
        emoji,
        style: const TextStyle(fontSize: 22),
      ),
    );
  }

  /// Человекочитаемое название ноды для списка.
  String _displayName(VpnNode node) {
    if (node.name.isNotEmpty) return node.name;
    return node.serverHost.isNotEmpty ? node.serverHost : 'Сервер #${node.id}';
  }

  /// Загрузка в процентах: online * 100 / 40, clamped [0;100].
  int _loadPercent(VpnNode node) {
    final raw = node.online * 100 / 40.0;
    var v = raw.round();
    if (v < 0) v = 0;
    if (v > 100) v = 100;
    return v;
  }

  @override
  Widget build(BuildContext context) {
    Widget body;

    if (_loading) {
      body = const Center(
        child: CircularProgressIndicator.adaptive(),
      );
    } else if (_error != null) {
      body = Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                _error!,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 12),
              ElevatedButton(
                onPressed: _loadNodes,
                child: const Text('Повторить'),
              ),
            ],
          ),
        ),
      );
    } else if (_nodes.isEmpty) {
      body = RefreshIndicator(
        onRefresh: _loadNodes,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.all(24),
          children: const [
            Text('Нет доступных серверов'),
          ],
        ),
      );
    } else {
      body = RefreshIndicator(
        onRefresh: _loadNodes,
        child: ListView.separated(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.all(16),
          separatorBuilder: (_, __) => const SizedBox(height: 12),
          itemCount: _nodes.length,
          itemBuilder: (_, i) {
            final node = _nodes[i];
            final hp = hostPortFromNode(node);
            final addrText = hp != null ? '${hp.host}:${hp.port}' : 'address: n/a';
            final ping = node.pingMs;
            final pingText = ping != null ? '$ping ms' : '— ms';
            final loadPercent = _loadPercent(node);

            return Container(
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                color: kSurface,
                borderRadius: BorderRadius.circular(kRadiusXXL),
                border: Border.all(color: kBorder),
              ),
              child: Row(
                children: [
                  _buildFlag(node.country),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Flexible(
                              child: Text(
                                _displayName(node),
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                            if (node.premium) ...[
                              const SizedBox(width: 8),
                              Container(
                                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                                decoration: BoxDecoration(
                                  color: kSurface,
                                  borderRadius: BorderRadius.circular(999),
                                  border: Border.all(color: kBorder),
                                ),
                                child: const Text(
                                  'PREMIUM',
                                  style: TextStyle(
                                    fontSize: 11,
                                    letterSpacing: 0.4,
                                    fontWeight: FontWeight.w700,
                                  ),
                                ),
                              ),
                            ],
                          ],
                        ),
                        const SizedBox(height: 2),
                        Text(
                          addrText,
                          style: const TextStyle(
                            fontSize: 12,
                            color: Colors.white,
                          ),
                        ),
                        const SizedBox(height: 4),
                        Text(
                          'Загруженность: $loadPercent%',
                          style: const TextStyle(
                            fontSize: 12,
                            color: Colors.white,
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  Text(
                    pingText,
                    style: const TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: () {
                      Navigator.of(context).pop(node);
                    },
                    child: const Text('Выбрать'),
                  ),
                ],
              ),
            );
          },
        ),
      );
    }

    return Scaffold(
      appBar: AppBar(title: const Text('Выбрать сервер')),
      body: body,
    );
  }
}

