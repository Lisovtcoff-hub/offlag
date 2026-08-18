import 'package:shared_preferences/shared_preferences.dart';

class ServicesStore {
  static const _kExcluded = 'excluded_services';

  /// Загрузить множество slug-ключей исключённых сервисов.
  static Future<Set<String>> loadExcluded() async {
    final p = await SharedPreferences.getInstance();
    final list = p.getStringList(_kExcluded) ?? const <String>[];
    return list.toSet();
  }

  /// Сохранить множество slug-ключей исключённых сервисов.
  static Future<void> saveExcluded(Set<String> excluded) async {
    final p = await SharedPreferences.getInstance();
    await p.setStringList(_kExcluded, excluded.toList());
  }
}
