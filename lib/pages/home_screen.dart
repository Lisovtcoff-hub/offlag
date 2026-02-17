import 'dart:io' show Platform;
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import '../theme.dart';
import 'services.dart';
// import 'servers.dart'; // больше не нужен здесь напрямую
import '../models/user_profile.dart';
import '../widgets/widgets.dart';

/// Домашний экран.
///
/// Показывает приветствие, статус подключения (пинг и страна при активном
/// соединении), основной тумблер включения/выключения и навигационные кнопки
/// к списку серверов и разделу «Сервисы».
class HomeScreen extends StatelessWidget {
  const HomeScreen({
    super.key,
    required this.connected,
    required this.onToggleConnection,
    required this.me,
    required this.loadingMe,
    required this.onRefreshMe,
    required this.countryLabel,
    required this.countryCode,
    required this.pingMs,
    required this.onOpenServers, // 👈 новый колбэк
  });

  /// Текущее состояние соединения.
  final bool connected;

  /// Колбэк переключения соединения.
  final VoidCallback onToggleConnection;

  /// Профиль пользователя; может быть `null`, если ещё не загружен.
  final UserProfile? me;

  /// Флаг загрузки профиля.
  final bool loadingMe;

  /// Обновление данных пользователя по жесту pull-to-refresh.
  final Future<void> Function() onRefreshMe;

  /// Человекочитаемое название выбранной ноды (например, `NL #1`).
  final String countryLabel;

  /// Двухбуквенный код страны (например, `NL`).
  final String countryCode;

  /// Измеренный пинг до выбранной ноды, мс.
  final int pingMs;

  /// Открыть экран выбора сервера (и там уже выбрать + подключиться).
  final Future<void> Function(BuildContext) onOpenServers;

  @override
  Widget build(BuildContext context) {
    final greetName = ((me?.nickname ?? '').isNotEmpty) ? me!.nickname : 'друг';
    final monthly = (me?.effectivePrice ?? 0) > 0 ? me!.effectivePrice : 60.0;
    final dailyCost = monthly / 30.0;
    final hasPremium = me?.premiumActive == true;
    final paymentsEnabled = me?.yookassaEnabled ?? true;
    final canUse = hasPremium || (me?.balance ?? 0.0) >= dailyCost;

    return RefreshIndicator(
      onRefresh: onRefreshMe,
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 0),
        children: [
          Row(
            children: [
              Container(
                width: 112,
                height: 48,
                decoration: BoxDecoration(
                  color: kSurface,
                  border: Border.all(color: kBorder),
                  borderRadius: BorderRadius.circular(kRadiusXL),
                ),
                alignment: Alignment.center,
                child: SizedBox(
                  width: 100,
                  height: 40,
                  child: SvgPicture.asset(
                    'assets/logo/logo_h.svg',
                    fit: BoxFit.contain,
                  ),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        'Привет, $greetName!',
                        style: const TextStyle(
                          fontSize: 22,
                          fontWeight: FontWeight.w700,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    if (loadingMe)
                      const SizedBox(
                        height: 18,
                        width: 18,
                        child: CircularProgressIndicator.adaptive(strokeWidth: 2),
                      ),
                  ],
                ),
              ),
            ],
          ),
          SizedBox(height: kBalanceHeight),
          SizedBox(
            height: kFlagHeight,
            child: Center(
              child: AnimatedSwitcher(
                duration: const Duration(milliseconds: 180),
                child: connected
                    ? PingBox(
                  key: ValueKey('$countryCode-$pingMs'),
                  countryLabel: countryLabel,
                  countryCode: countryCode.toLowerCase(),
                  pingMs: pingMs,
                )
                    : const SizedBox.shrink(),
              ),
            ),
          ),
          const SizedBox(height: 22),
          Center(
            child: SizedBox(
              width: Ui.tumblerWidth(context),
                child: OnOffSwitch(
                  value: connected,
                  enabled: true,
                  onChanged: (_) {
                    if (!canUse) {
                      _showTopUp(context, dailyCost, paymentsEnabled);
                      return;
                    }
                    onToggleConnection();
                  },
                ),
              ),
            ),
          const SizedBox(height: 11),
          Center(
            child: SizedBox(
              width: Ui.mainWidth(context),
              child: Text(
                'Автоматически найдем сервер с наименьшей задержкой!',
                textAlign: TextAlign.center,
                softWrap: true,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Colors.white,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ),
          const SizedBox(height: 22),
          Center(
            child: SizedBox(
              width: Ui.mainWidth(context),
              child: Text(
                'Чтобы выбрать сервер вручную перейдите в список серверов',
                textAlign: TextAlign.center,
                softWrap: true,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Colors.white,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ),
          const SizedBox(height: 11),
          Center(
            child: SizedBox(
              width: Ui.mainWidth(context),
              height: Ui.mainWidth(context) * 0.18,
              child: ElevatedButton(
                // 👇 вместо прямого Navigator.push вызываем колбэк
                onPressed: () => onOpenServers(context),
                child: const Text('Список серверов'),
              ),
            ),
          ),
          const SizedBox(height: 22),
          Center(
            child: SizedBox(
              width: Ui.mainWidth(context),
              height: Ui.mainWidth(context) * 0.18,
              child: ElevatedButton(
                onPressed: () {
                  if (Platform.isAndroid) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('В разработке. Будет доступно в следующих обновлениях'),
                      ),
                    );
                    return;
                  }
                  Navigator.of(context).push(
                    MaterialPageRoute(builder: (_) => const ServicesPage()),
                  );
                },
                child: const Text('Сервисы'),
              ),
            ),
          ),
        ],
      ),
    );
  }

  /// Показывает модальное окно с требованием пополнить баланс,
  /// если на счёте меньше дневной стоимости [dailyCost].
  void _showTopUp(BuildContext context, double dailyCost, bool paymentsEnabled) {
    if (!paymentsEnabled) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Оплата временно недоступна')),
      );
      return;
    }
    showDialog(
      context: context,
      builder: (ctx) => NativeDialogFrame(
        title: const Text(
          'Недостаточно средств',
          style: TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
        content: Text(
          'Для активации нужно минимум ${dailyCost.toStringAsFixed(2)} ₽ на балансе.\n'
              'Пополните баланс, чтобы включить ускорение.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('ОК'),
          ),
        ],
      ),
    );
  }
}

