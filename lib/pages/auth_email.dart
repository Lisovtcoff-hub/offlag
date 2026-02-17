/// Экран ввода e-mail для отправки одноразового кода.
///
/// Предоставляет текстовое поле для почты и кнопку,
/// по нажатию на которую бэкенд отправляет код подтверждения.
/// При успешной отправке открывает экран ввода кода [`CodeScreen`].
import 'package:flutter/material.dart';
import '../theme.dart';
import '../net.dart';
import 'auth_code.dart';

/// Страница «Вход по e-mail».
class AuthEmailPage extends StatefulWidget {
  const AuthEmailPage({super.key});

  @override
  State<AuthEmailPage> createState() => _AuthEmailPageState();
}

class _AuthEmailPageState extends State<AuthEmailPage> {
  final _ctrl = TextEditingController();
  bool _sending = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  /// Отправляет одноразовый код на указанный e-mail.
  ///
  /// Валидирует, что поле не пустое; отображает состояния загрузки и
  /// сообщения об успехе/ошибке через `SnackBar`. При успехе выполняет
  /// навигацию на [`CodeScreen`] с переданным `email`.
  Future<void> _send() async {
    final email = _ctrl.text.trim();
    if (email.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Введите вашу почту')),
      );
      return;
    }
    setState(() => _sending = true);
    try {
      await dio.post('/send_code', data: {'email': email});
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Код отправлен на $email')),
      );
      Navigator.of(context).pushReplacement(
        MaterialPageRoute(builder: (_) => CodeScreen(email: email)),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Ошибка отправки кода')),
      );
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  /// Строит интерфейс страницы:
  /// - `AppBar` с заголовком;
  /// - поле ввода e-mail с тёмной темой;
  /// - кнопка «Получить код» с индикатором прогресса.
  @override
  Widget build(BuildContext context) {
    final w = Ui.mainWidth(context);
    return Scaffold(
      backgroundColor: kBg,
      appBar: AppBar(title: const Text('Вход по e-mail')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 24, 20, 24),
        children: [
          Text('Укажите e-mail', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 12),
          TextField(
            controller: _ctrl,
            style: const TextStyle(color: kInk),
            keyboardType: TextInputType.emailAddress,
            decoration: InputDecoration(
              hintText: 'you@example.com',
              hintStyle: const TextStyle(color: Colors.white),
              filled: true,
              fillColor: kSurface,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(14),
                borderSide: const BorderSide(color: kBorder),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(14),
                borderSide: const BorderSide(color: kBorder),
              ),
              focusedBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(14),
                borderSide: const BorderSide(color: Colors.white),
              ),
            ),
          ),
          const SizedBox(height: 16),
          SizedBox(
            width: w,
            height: w * 0.18,
            child: ElevatedButton(
              onPressed: _sending ? null : _send,
              child: _sending
                  ? const SizedBox(
                width: 22,
                height: 22,
                child: CircularProgressIndicator.adaptive(strokeWidth: 3),
              )
                  : const Text('Получить код'),
            ),
          ),
        ],
      ),
    );
  }
}

