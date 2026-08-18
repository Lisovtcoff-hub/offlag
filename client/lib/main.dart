import 'dart:io' show Platform;
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'net.dart';
import 'theme.dart';
import 'pages/welcome.dart';
import 'pages/main_tabs.dart';
import 'storage/token_store.dart';
import 'vpn_controller.dart';

/// Главная функция приложения.
///
/// Выполняет предварительную инициализацию, настраивает параметры окна
/// для десктопа (фиксированный размер 460×800, центрирование, запрет ресайза),
/// затем запускает корневой виджет [`OffLagApp`].
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // При старте на Windows гарантированно выключаем все xray.exe,
  // чтобы тумблер всегда отражал реальное состояние (VPN off).
  if (Platform.isWindows) {
    await VpnController.killAllXray();
  }

  if (!kIsWeb && (Platform.isWindows || Platform.isLinux || Platform.isMacOS)) {
    await windowManager.ensureInitialized();

    const appSize = Size(460, 800);

    final opts = const WindowOptions(
      size: appSize,
      center: true,
      titleBarStyle: TitleBarStyle.normal,
      backgroundColor: Colors.transparent,
    );

    await windowManager.waitUntilReadyToShow(opts, () async {
      await windowManager.setResizable(false);
      await windowManager.setMinimumSize(appSize);
      await windowManager.setMaximumSize(appSize);
      await windowManager.show();
      await windowManager.focus();
    });
  }

  runApp(const OffLagApp());
}

/// Корневой виджет приложения OffLag.
class OffLagApp extends StatelessWidget {
  const OffLagApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'OffLag',
      debugShowCheckedModeBanner: false,
      theme: buildAppTheme(),
      home: const _Boot(),
    );
  }
}

/// Промежуточный загрузочный экран.
class _Boot extends StatefulWidget {
  const _Boot();

  @override
  State<_Boot> createState() => _BootState();
}

class _BootState extends State<_Boot> {
  @override
  void initState() {
    super.initState();
    _go();
  }

  /// Проверяет наличие токена в [TokenStore] и выполняет навигацию.
  Future<void> _go() async {
    final tok = await TokenStore.token;
    final mail = await TokenStore.email;
    final refresh = await TokenStore.refreshToken;

    if (!mounted) return;
    if (tok != null && tok.isNotEmpty) {
      Session.token = tok;
      Session.email = mail;

      Navigator.of(context).pushReplacement(
        MaterialPageRoute(builder: (_) => const MainTabs()),
      );
    } else if (refresh != null && refresh.isNotEmpty) {
      final ok = await refreshSession();
      if (!mounted) return;
      if (ok) {
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const MainTabs()),
        );
      } else {
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const WelcomePage()),
        );
      }
    } else {
      Navigator.of(context).pushReplacement(
        MaterialPageRoute(builder: (_) => const WelcomePage()),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      body: Center(child: CircularProgressIndicator.adaptive()),
    );
  }
}
