import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'native_sheet_style.dart';

/// BottomSheet ввода промокода в формате `XXXX-XXXX-XXXX-XXXX`.
///
/// Валидирует ввод, поддерживает вставку из буфера обмена и маску форматирования.
/// По подтверждению возвращает введённый код через `Navigator.pop(code)`.
class PromoCodeSheet extends StatefulWidget {
  const PromoCodeSheet({super.key});

  @override
  State<PromoCodeSheet> createState() => _PromoCodeSheetState();
}

class _PromoCodeSheetState extends State<PromoCodeSheet> {
  /// Контроллер текстового поля промокода.
  final _ctrl = TextEditingController();

  /// Ключ формы для валидации.
  final _formKey = GlobalKey<FormState>();

  /// Флаг отправки (резерв под асинхронную обработку).
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _ctrl.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  /// Вставляет промокод из буфера обмена и приводит его к формату маски.
  Future<void> _paste() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final txt = (data?.text ?? '').toUpperCase().replaceAll(RegExp(r'[^A-Z0-9]'), '');
    if (txt.isEmpty) return;
    final buf = StringBuffer();
    for (int i = 0; i < txt.length && i < 16; i++) {
      if (i > 0 && i % 4 == 0) buf.write('-');
      buf.write(txt[i]);
    }
    _ctrl.text = buf.toString();
    _ctrl.selection = TextSelection.collapsed(offset: _ctrl.text.length);
  }

  /// Проверяет, что строка промокода соответствует шаблону `XXXX-XXXX-XXXX-XXXX`.
  String? _validator(String? v) {
    if (v == null || v.isEmpty) return 'Введите промокод';
    final ok = RegExp(r'^[A-Z0-9]{4}(-[A-Z0-9]{4}){3}$').hasMatch(v.trim());
    if (!ok) return 'Формат: XXXX-XXXX-XXXX-XXXX';
    return null;
  }

  /// Разметка шита с полем ввода, кнопкой вставки и подтверждением.
  @override
  Widget build(BuildContext context) {
    final canSubmit = RegExp(r'^[A-Z0-9]{4}(-[A-Z0-9]{4}){3}$').hasMatch(_ctrl.text.trim());

    return Padding(
      padding: nativeSheetPadding(context),
      child: Form(
        key: _formKey,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            nativeSheetTitle(context, 'Промокод'),
            const SizedBox(height: 12),
            TextFormField(
              controller: _ctrl,
              textCapitalization: TextCapitalization.characters,
              inputFormatters: [_PromoMaskFormatter()],
              decoration: nativeSheetInputDecoration(hintText: 'XXXX-XXXX-XXXX-XXXX'),
              validator: _validator,
            ),
            const SizedBox(height: 10),
            Row(
              children: [
                TextButton.icon(
                  onPressed: _paste,
                  icon: const Icon(Icons.paste),
                  label: const Text('Вставить из буфера'),
                ),
                const Spacer(),
                FilledButton(
                  onPressed: !_submitting && canSubmit
                      ? () {
                    if (_formKey.currentState?.validate() ?? false) {
                      Navigator.of(context).pop(_ctrl.text.trim());
                    }
                  }
                      : null,
                  child: _submitting
                      ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator.adaptive(strokeWidth: 2))
                      : const Text('Готово'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// Форматтер, приводящий ввод к маске промокода `XXXX-XXXX-XXXX-XXXX`.
class _PromoMaskFormatter extends TextInputFormatter {
  @override
  TextEditingValue formatEditUpdate(TextEditingValue oldV, TextEditingValue newV) {
    final raw = newV.text.toUpperCase().replaceAll(RegExp(r'[^A-Z0-9]'), '');
    final buf = StringBuffer();
    for (int i = 0; i < raw.length && i < 16; i++) {
      if (i > 0 && i % 4 == 0) buf.write('-');
      buf.write(raw[i]);
    }
    final s = buf.toString();
    return TextEditingValue(
      text: s,
      selection: TextSelection.collapsed(offset: s.length),
      composing: TextRange.empty,
    );
  }
}

