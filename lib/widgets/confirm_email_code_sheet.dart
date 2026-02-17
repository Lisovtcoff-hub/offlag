import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'native_sheet_style.dart';

/// BottomSheet подтверждения смены e-mail одноразовым кодом (6 цифр).
///
/// Показывает адрес, на который выслан код, поле ввода и кнопки
/// «Вставить код» (из буфера обмена) и «Готово». По подтверждению
/// возвращает введённый код через `Navigator.pop(code)`.
class ConfirmEmailCodeSheet extends StatefulWidget {
  /// Адрес почты, на который был отправлен код подтверждения.
  final String email;

  const ConfirmEmailCodeSheet({super.key, required this.email});

  @override
  State<ConfirmEmailCodeSheet> createState() => _ConfirmEmailCodeSheetState();
}

class _ConfirmEmailCodeSheetState extends State<ConfirmEmailCodeSheet> {
  /// Контроллер для поля ввода 6-значного кода.
  final _ctrl = TextEditingController();

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  /// Берёт текст из буфера обмена, оставляет только цифры и подставляет
  /// первые 6 символов в поле ввода.
  Future<void> _paste() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final txt = (data?.text ?? '').replaceAll(RegExp(r'\D'), '').trim();
    if (txt.isNotEmpty) {
      _ctrl.text = txt.substring(0, txt.length.clamp(0, 6));
      _ctrl.selection = TextSelection.collapsed(offset: _ctrl.text.length);
    }
  }

  /// Разметка шита: заголовок, e-mail, поле кода и кнопки действий.
  @override
  Widget build(BuildContext context) {
    final canSubmit = RegExp(r'^\d{6}$').hasMatch(_ctrl.text.trim());
    return Padding(
      padding: nativeSheetPadding(context),
      child: StatefulBuilder(
        builder: (ctx, setSt) => Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            nativeSheetTitle(context, 'Подтвердите e-mail'),
            const SizedBox(height: 6),
            Center(
              child: Text(
                widget.email,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(color: Colors.white),
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _ctrl,
              keyboardType: TextInputType.number,
              maxLength: 6,
              decoration: nativeSheetInputDecoration(
                hintText: 'Код из письма (6 цифр)',
                counterText: '',
              ),
              onChanged: (_) => setSt(() {}),
            ),
            const SizedBox(height: 10),
            Row(
              children: [
                TextButton.icon(
                  onPressed: _paste,
                  icon: const Icon(Icons.paste),
                  label: const Text('Вставить код'),
                ),
                const Spacer(),
                FilledButton(
                  onPressed: canSubmit ? () => Navigator.of(context).pop(_ctrl.text.trim()) : null,
                  child: const Text('Готово'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
