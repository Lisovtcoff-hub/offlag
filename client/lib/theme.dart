import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:animations/animations.dart';

// ===== Цвета (тёмная тема) =====
const kBg = Color(0xFF1F1F1F);
const kSurface = Color(0xFF2B2B2B);
const kBorder = Color(0xFF3D3D3D);
const kInk = Colors.white;

const kRadiusXL = 24.0;
const kRadiusXXL = 32.0;

// ===== Константы разметки =====
const kBalanceHeight = 164.0;
const kFlagHeight = 44.0;

class Ui {
  static const double ovalH = 76;
  static const brRadius = 32.0;
  static const pH = EdgeInsets.symmetric(horizontal: 16, vertical: 10);
  static BorderRadius get br => BorderRadius.circular(brRadius);
  static double mainWidth(BuildContext context) =>
      MediaQuery.of(context).size.width * 0.7;
  static double tumblerWidth(BuildContext context) =>
      MediaQuery.of(context).size.width * 0.55;
}

// ===== Тема =====
ThemeData buildAppTheme() {
  final colorScheme = ColorScheme(
    brightness: Brightness.dark,
    primary: Colors.white,
    onPrimary: Colors.black,
    secondary: Colors.white,
    onSecondary: Colors.black,
    surface: kSurface,
    onSurface: Colors.white,
    error: Colors.white,
    onError: Colors.black,
  );

  const textTheme = TextTheme(
    headlineLarge: TextStyle(fontSize: 28, fontWeight: FontWeight.w700, color: kInk),
    headlineMedium: TextStyle(fontSize: 24, fontWeight: FontWeight.w700, color: kInk),
    titleLarge: TextStyle(fontSize: 20, fontWeight: FontWeight.w700, color: kInk),
    titleMedium: TextStyle(fontSize: 18, fontWeight: FontWeight.w600, color: kInk),
    bodyLarge: TextStyle(fontSize: 16, color: kInk),
    bodyMedium: TextStyle(fontSize: 15, color: kInk),
    labelLarge: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: kInk),
  );

  return ThemeData(
    useMaterial3: true,
    colorScheme: colorScheme,
    scaffoldBackgroundColor: kBg,
    textTheme: textTheme,
    iconTheme: const IconThemeData(color: Colors.white),
    appBarTheme: const AppBarTheme(
      backgroundColor: kBg,
      elevation: 0,
      surfaceTintColor: Colors.transparent,
      foregroundColor: Colors.white,
      titleTextStyle: TextStyle(fontSize: 20, fontWeight: FontWeight.w700, color: Colors.white),
    ),
    dividerColor: kBorder,

    // ⬇️ ГЛОБАЛЬНЫЕ ПЕРЕХОДЫ (FadeThrough)
    pageTransitionsTheme: const PageTransitionsTheme(builders: {
      TargetPlatform.android: FadeThroughPageTransitionsBuilder(),
      TargetPlatform.windows: FadeThroughPageTransitionsBuilder(),
      TargetPlatform.linux: FadeThroughPageTransitionsBuilder(),
      TargetPlatform.fuchsia: FadeThroughPageTransitionsBuilder(),
      TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),   // нативно для iOS
      TargetPlatform.macOS: CupertinoPageTransitionsBuilder(), // и для macOS
    }),

    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ElevatedButton.styleFrom(
        backgroundColor: const Color(0xFF383838),
        foregroundColor: kInk,
        shadowColor: Colors.transparent,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(44)),
        side: const BorderSide(color: kBorder),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: kInk,
        side: const BorderSide(color: kBorder, width: 1.4),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(kRadiusXXL)),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
      ),
    ),
    chipTheme: ChipThemeData(
      backgroundColor: kSurface,
      selectedColor: Colors.white,
      side: const BorderSide(color: kBorder),
      labelStyle: const TextStyle(color: kInk, fontWeight: FontWeight.w700),
      shape: const StadiumBorder(side: BorderSide(color: kBorder)),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
    ),
    switchTheme: const SwitchThemeData(
      trackColor: WidgetStatePropertyAll(kSurface),
      thumbColor: WidgetStatePropertyAll(Colors.white),
    ),
    snackBarTheme: const SnackBarThemeData(
      backgroundColor: kSurface,
      contentTextStyle: TextStyle(color: kInk, fontWeight: FontWeight.w700),
      behavior: SnackBarBehavior.floating,
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: Colors.white,
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      hintStyle: const TextStyle(color: Colors.white),
      errorStyle: const TextStyle(color: Colors.white),
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
        borderSide: const BorderSide(color: kBorder, width: 1.4),
      ),
      errorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: const BorderSide(color: kBorder),
      ),
      focusedErrorBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: const BorderSide(color: kBorder, width: 1.4),
      ),
    ),
    textSelectionTheme: const TextSelectionThemeData(
      cursorColor: Colors.white,
      selectionColor: Color(0x33FFFFFF),
      selectionHandleColor: Colors.white,
    ),
  );

}
