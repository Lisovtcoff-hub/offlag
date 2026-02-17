/// Стартовая страница приложения OffLag.
///
/// Показывает логотип и короткий подзаголовок, а также кнопку входа,
/// которая ведёт на экран аутентификации по e-mail.
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../theme.dart';
import 'auth_email.dart';
import '../widgets/widgets.dart';

/// Экран приветствия с логотипом и переходом на авторизацию.
class WelcomePage extends StatefulWidget {
  const WelcomePage({super.key});

  @override
  State<WelcomePage> createState() => _WelcomePageState();
}

class _WelcomePageState extends State<WelcomePage> {
  bool _acceptedPrivacy = false;

  Future<void> _openPrivacy() async {
    const url = 'https://offlag.ru/docs/privacy_policy';
    final uri = Uri.parse(url);

    if (!await launchUrl(uri, mode: LaunchMode.externalApplication)) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Не удалось открыть Политику конфиденциальности и Пользовательское соглашение')),
      );
    }
  }

  void _goToAuth() {
    if (!_acceptedPrivacy) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Чтобы продолжить, отметьте согласие с Политикой конфиденциальности и Пользовательским соглашением'),
        ),
      );
      return;
    }

    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const AuthEmailPage()),
    );
  }

  @override
  Widget build(BuildContext context) {
    final w = Ui.mainWidth(context);

    return Scaffold(
      backgroundColor: kBg,
      body: Center(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 20),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Container(
                width: 180,
                height: 80,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  color: const Color.fromRGBO(61, 61, 61, 1.0),
                  borderRadius: BorderRadius.circular(24),
                  border: Border.all(color: kBorder),
                ),
                child: const AnimatedWebpOnce(
                  asset:  'assets/anim/logo.webp',
                  poster: 'assets/anim/logo_lastframe.png',
                  duration: Duration(seconds: 2),
                  width: 150,
                  height: 60,
                  fit: BoxFit.contain,
                  borderRadius: 0,
                ),
              ),
              const SizedBox(height: 18),
              Text(
                'Ничего лишнего: скорость, безопасность, выгода',
                textAlign: TextAlign.center,
                softWrap: true,
                style: Theme.of(context).textTheme.headlineLarge,
              ),
              const SizedBox(height: 24),

              // 👉 Галочка "Согласен с Политикой конфиденциальности"
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(
                    width: 24,
                    height: 24,
                    child: Checkbox(
                      value: _acceptedPrivacy,
                      onChanged: (v) {
                        setState(() => _acceptedPrivacy = v ?? false);
                      },
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: GestureDetector(
                      onTap: _openPrivacy,
                      child: RichText(
                        text: TextSpan(
                          style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                            color: Colors.white,
                          ),
                          children: const [
                            TextSpan(text: 'Я соглашаюсь с '),
                            TextSpan(
                              text: 'Пользовательским соглашением и Политикой конфиденциальности',
                              style: TextStyle(
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              ),

              const SizedBox(height: 24),
              SizedBox(
                width: w,
                height: w * 0.18,
                child: ElevatedButton(
                  onPressed: _goToAuth,
                  child: const Text('Войти'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
