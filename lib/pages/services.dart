import 'package:flutter/material.dart';
import '../theme.dart';
import '../storage/services_store.dart';

/// Предустановленные сервисы, которые пользователь может исключить из действия OffLag.
const List<String> kServiceLabels = [
  'Steam',
  'Epic Games',
  'YouTube',
  'Discord',
  'Telegram',
  'Twitch',
  'Luma',
];

/// Экран управления исключениями сервисов.
///
/// Позволяет выбрать сайты/платформы, трафик которых не нужно ускорять.
/// Список представлен переключателями со статусами «Исключить/Исключён».
class ServicesPage extends StatefulWidget {
  const ServicesPage({super.key});

  @override
  State<ServicesPage> createState() => _ServicesPageState();
}

class _ServicesPageState extends State<ServicesPage> {
  /// Множество slug-ключей сервисов, исключённых пользователем.
  final Set<String> _excluded = <String>{};

  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _loadInitial();
  }

  /// Загружаем сохранённые исключения из ServicesStore.
  Future<void> _loadInitial() async {
    final saved = await ServicesStore.loadExcluded();
    if (!mounted) return;
    setState(() {
      _excluded
        ..clear()
        ..addAll(saved);
      _loading = false;
    });
  }

  /// Преобразует произвольную метку [s] к slug (`lowercase-kebab-case`).
  String _slug(String s) =>
      s.toLowerCase().replaceAll(RegExp(r'[^a-z0-9]+'), '-').replaceAll(RegExp(r'^-+|-+$'), '');

  /// Переключает сервис с ключом [key] в состояние исключения [v].
  void _toggle(String key, bool v) {
    setState(() {
      if (v) {
        _excluded.add(key);
      } else {
        _excluded.remove(key);
      }
    });
    // сохраняем изменения через стор
    ServicesStore.saveExcluded(_excluded);
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return Scaffold(
        appBar: AppBar(title: const Text('Сервисы')),
        body: const Center(child: CircularProgressIndicator.adaptive()),
      );
    }

    return Scaffold(
      appBar: AppBar(
        title: const Text('Сервисы'),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(26),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                _excluded.isEmpty
                    ? 'Все сервисы ускоряются OffLag'
                    : 'Исключено: ${_excluded.length}',
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Colors.white,
                ),
              ),
            ),
          ),
        ),
      ),
      body: ListView.separated(
        padding: const EdgeInsets.all(16),
        separatorBuilder: (_, __) => const SizedBox(height: 12),
        itemCount: kServiceLabels.length + 1,
        itemBuilder: (context, index) {
          if (index == 0) {
            return Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text(
                'Выберите один или несколько сайтов для исключения их из зоны действия OffLag.',
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Colors.white,
                  fontWeight: FontWeight.w600,
                ),
              ),
            );
          }
          final label = kServiceLabels[index - 1];
          final key = _slug(label);
          final selected = _excluded.contains(key);

          return InkWell(
            borderRadius: BorderRadius.circular(kRadiusXXL),
            onTap: () => _toggle(key, !selected),
            child: Container(
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                color: kSurface,
                borderRadius: BorderRadius.circular(kRadiusXXL),
                border: Border.all(color: kBorder),
              ),
              child: Row(
                children: [
                  Switch(
                    value: selected,
                    onChanged: (v) => _toggle(key, v),
                    thumbColor: const WidgetStatePropertyAll(Colors.white),
                    trackColor: const WidgetStatePropertyAll(kSurface),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      label,
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                  ),
                  Text(
                    selected ? 'Исключён' : 'Исключить',
                    style: TextStyle(
                      color: Colors.white,
                      fontWeight: selected ? FontWeight.w700 : FontWeight.w400,
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

