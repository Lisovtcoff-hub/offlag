import 'package:flutter/material.dart';
import 'package:dio/dio.dart';
import 'package:url_launcher/url_launcher.dart';

import '../theme.dart';
import '../net.dart';
import 'native_sheet_style.dart';

/// BottomSheet пополнения баланса.
///
/// Двухшаговый сценарий:
/// 1) выбор суммы (пресет или ввод вручную);
/// 2) оплата банковской картой через YooMoney/YooKassa.
class TopUpSheet extends StatefulWidget {
  const TopUpSheet({
    super.key,
    this.onPaymentStarted,
  });

  /// Вызывается, когда платёж успешно создан и страница YooMoney/YooKassa открыта.
  final VoidCallback? onPaymentStarted;

  @override
  State<TopUpSheet> createState() => _TopUpSheetState();
}

class _TopUpSheetState extends State<TopUpSheet> {
  /// Предустановленные суммы пополнения.
  final _presetAmounts = const [60, 180, 360, 720];

  /// Итоговая выбранная сумма (в рублях).
  int? _selectedAmount;

  /// Текущий шаг мастера (1 — сумма, 2 — способ оплаты).
  int _step = 1;

  /// Флаг процесса создания платежа / открытия ссылки.
  bool _isProcessing = false;

  /// Контроллер для ручного ввода суммы.
  final TextEditingController _manualAmountController =
  TextEditingController();

  /// Текст ошибки для поля ручного ввода (показывается под TextField).
  String? _amountErrorText;

  @override
  void dispose() {
    _manualAmountController.dispose();
    super.dispose();
  }

  /// Проверяет, выбрана ли сумма/введена вручную, и переходит на шаг 2.
  void _goToPaymentStep() {
    final manualText = _manualAmountController.text.trim();

    int? amount;

    if (manualText.isNotEmpty) {
      final parsed = int.tryParse(manualText);
      if (parsed == null || parsed <= 0) {
        setState(() {
          _amountErrorText = 'Введите корректную сумму в рублях';
        });
        return;
      }
      amount = parsed;
    } else {
      amount = _selectedAmount;
    }

    if (amount == null) {
      setState(() {
        _amountErrorText = 'Выберите сумму или введите её вручную';
      });
      return;
    }

    setState(() {
      _amountErrorText = null;
      _selectedAmount = amount;
      _step = 2;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: nativeSheetPadding(context, top: 8, bottom: 16),
      child: AnimatedSize(
        duration: const Duration(milliseconds: 200),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            nativeSheetTitle(context, _step == 1 ? 'Выберите сумму' : 'Оплата банковской картой'),
            const SizedBox(height: 14),
            if (_step == 1) ...[
              // Пресеты
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: _presetAmounts.map((v) {
                  final sel = _selectedAmount == v;
                  return ChoiceChip(
                    label: Text('$v ₽'),
                    selected: sel,
                    onSelected: (selected) {
                      if (!selected) return;
                      setState(() {
                        _selectedAmount = v;
                        _manualAmountController.text = v.toString();
                        _amountErrorText = null;
                      });
                    },
                  );
                }).toList(),
              ),
              const SizedBox(height: 16),

              // Поле ручного ввода
              Align(
                alignment: Alignment.centerLeft,
                child: Text(
                  'Или введите сумму вручную',
                  style: Theme.of(context)
                      .textTheme
                      .bodySmall
                      ?.copyWith(color: Colors.white),
                ),
              ),
              const SizedBox(height: 6),
              TextField(
                controller: _manualAmountController,
                keyboardType: TextInputType.number,
                decoration: nativeSheetInputDecoration(
                  hintText: 'Например, 150',
                  errorText: _amountErrorText,
                ),
                onChanged: (value) {
                  setState(() {
                    // Как только пользователь начал что-то печатать —
                    // считаем, что он вводит свою сумму, и серый чип убираем.
                    if (value.trim().isNotEmpty) {
                      _selectedAmount = null;
                    }
                    _amountErrorText = null;
                  });
                },
              ),
              const SizedBox(height: 14),
              ElevatedButton(
                onPressed: _goToPaymentStep,
                child: const Text('Далее'),
              ),
            ] else ...[
              const SizedBox(height: 4),
              if (_selectedAmount != null)
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Text(
                    'К оплате: ${_selectedAmount} ₽',
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ),
              PayMethodTile(
                title: 'Оплата банковской картой (YooMoney)',
                subtitle: 'VISA / MasterCard / МИР',
                icon: Icons.credit_card_rounded,
                trailing: _isProcessing
                    ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator.adaptive(strokeWidth: 2),
                )
                    : const Icon(
                  Icons.chevron_right_rounded,
                  color: Colors.white,
                ),
                onTap: () {
                  if (_isProcessing) return;
                  _payViaYooMoney(context);
                },
              ),
              const SizedBox(height: 8),
              Text(
                'После успешной оплаты вернитесь в приложение — '
                    'баланс скоро обновится автоматически.',
                style: Theme.of(context)
                    .textTheme
                    .bodySmall
                    ?.copyWith(color: Colors.white),
              ),
              const SizedBox(height: 8),
              InkWell(
                onTap: () => _openPublicOffer(context),
                child: Text(
                  'Продолжая, вы соглашаетесь с Публичной офертой',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Colors.white,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  /// Реальная оплата через YooMoney/YooKassa:
  /// 1) дергает /payments/yookassa/create на бэке;
  /// 2) получает confirmation_url;
  /// 3) открывает браузер с оплатой.
  Future<void> _payViaYooMoney(BuildContext ctx) async {
    final amount = _selectedAmount;
    if (amount == null || amount <= 0) {
      // Это теоретический случай, на шаге 2 такого быть не должно,
      // поэтому можно показать обычный SnackBar.
      ScaffoldMessenger.of(ctx).showSnackBar(
        const SnackBar(
          content: Text('Сумма не выбрана. Вернитесь назад и выберите сумму.'),
        ),
      );
      return;
    }

    setState(() => _isProcessing = true);

    try {
      final res = await dio.post('/payments/yookassa/create', data: {
        'amount': amount.toDouble(),
        'description': 'Пополнение баланса в приложении',
      });

      if (!ctx.mounted) return;
      final data = res.data as Map<String, dynamic>? ?? {};
      final url = (data['confirmation_url'] as String?) ?? '';

      if (url.isEmpty) {
        ScaffoldMessenger.of(ctx).showSnackBar(
          const SnackBar(content: Text('Не удалось получить ссылку на оплату')),
        );
        return;
      }

      final uri = Uri.parse(url);

      ScaffoldMessenger.of(ctx).showSnackBar(
        const SnackBar(
          content: Text(
            'Сейчас откроется страница оплаты. После оплаты вернитесь в приложение.',
          ),
        ),
      );

      final ok = await launchUrl(
        uri,
        mode: LaunchMode.externalApplication,
      );

      if (!ctx.mounted) return;
      if (!ok) {
        ScaffoldMessenger.of(ctx).showSnackBar(
          const SnackBar(content: Text('Не удалось открыть страницу оплаты')),
        );
        return;
      }

      // Платёж успешно создан и страница оплаты открыта.
      // Сообщаем профилю, чтобы он начал авто-обновление.
      widget.onPaymentStarted?.call();

      // Закрываем шит — пользователь ушёл в браузер.
      if (Navigator.canPop(ctx)) {
        Navigator.pop(ctx);
      }
    } catch (e) {
      if (!ctx.mounted) return;
      String msg = 'Ошибка при создании платежа';
      if (e is DioException) {
        final status = e.response?.statusCode;
        if (e.response?.data is Map) {
          final data = e.response?.data as Map;
          final err = data['error']?.toString().trim();
          if (err != null && err.isNotEmpty) {
            msg = err;
          }
        } else if (status == 503) {
          msg = 'Оплата временно недоступна';
        }
      }
      ScaffoldMessenger.of(ctx).showSnackBar(
        SnackBar(content: Text(msg)),
      );
    } finally {
      if (mounted) {
        setState(() => _isProcessing = false);
      }
    }
  }

  /// Открываем публичную оферту в браузере.
  Future<void> _openPublicOffer(BuildContext ctx) async {
    final uri = Uri.parse('https://offlag.ru/docs/public_offer');
    final ok = await launchUrl(
      uri,
      mode: LaunchMode.externalApplication,
    );
    if (!ctx.mounted) return;
    if (!ok) {
      ScaffoldMessenger.of(ctx).showSnackBar(
        const SnackBar(content: Text('Не удалось открыть публичную оферту')),
      );
    }
  }
}

/// Плитка способа оплаты со значком, заголовком и подзаголовком.
///
/// По тапу вызывает [onTap].
class PayMethodTile extends StatelessWidget {
  /// Отображаемое название метода.
  final String title;

  /// Краткое описание/список платёжных систем.
  final String subtitle;

  /// Иконка метода оплаты.
  final IconData icon;

  /// Колбэк при выборе.
  final VoidCallback onTap;

  /// Виджет справа (по умолчанию — стрелка).
  final Widget? trailing;

  const PayMethodTile({
    super.key,
    required this.title,
    required this.subtitle,
    required this.icon,
    required this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(kRadiusXXL),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: kSurface,
          borderRadius: BorderRadius.circular(kRadiusXXL),
          border: Border.all(color: kBorder),
        ),
        child: Row(
          children: [
            Container(
              width: 44,
              height: 44,
              decoration: BoxDecoration(
                color: const Color(0xFF383838),
                border: Border.all(color: kBorder),
                shape: BoxShape.circle,
              ),
              child: Icon(icon, color: kInk),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: const TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    subtitle,
                    style: const TextStyle(color: Colors.white),
                  ),
                ],
              ),
            ),
            trailing ??
                const Icon(
                  Icons.chevron_right_rounded,
                  color: Colors.white,
                ),
          ],
        ),
      ),
    );
  }
}

