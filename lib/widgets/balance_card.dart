import 'package:flutter/material.dart';
import '../theme.dart';

/// Карточка баланса с кнопкой пополнения и кнопкой скрытия.
///
/// Показывает две строки текста (баланс и «хватит до»), а также кнопку
/// «Пополнить». Справа поверх — иконка «Скрыть».
class BalanceCard extends StatelessWidget {
  /// Текст основной суммы баланса.
  final String balanceText;

  /// Текст-пояснение «Хватит до …».
  final String enoughText;

  /// Колбэк нажатия на кнопку «Пополнить».
  final VoidCallback onTopUp;

  /// Колбэк нажатия на иконку «Скрыть».
  final VoidCallback onToggleHide;

  const BalanceCard({
    super.key,
    required this.balanceText,
    required this.enoughText,
    required this.onTopUp,
    required this.onToggleHide,
  });

  /// Рендерит карточку с контентом и действиями.
  @override
  Widget build(BuildContext context) {
    return Stack(
      alignment: Alignment.centerRight,
      children: [
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: kSurface,
            borderRadius: BorderRadius.circular(kRadiusXXL),
            border: Border.all(color: kBorder),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(balanceText, style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 6),
              Text(enoughText, style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 12),
              ElevatedButton(onPressed: onTopUp, child: const Text('Пополнить')),
            ],
          ),
        ),
        Positioned(
          right: 10,
          child: IconButton(
            tooltip: 'Скрыть',
            onPressed: onToggleHide,
            icon: const Icon(Icons.visibility_off_rounded, color: Colors.white),
          ),
        ),
      ],
    );
  }
}
