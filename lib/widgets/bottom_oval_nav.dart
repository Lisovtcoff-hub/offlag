import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import '../theme.dart';

/// Овальная нижняя навигация с двумя круглыми кнопками.
///
/// Показывает две вкладки: «Главная» и «Профиль». Выделяет активную.
class BottomOvalNav extends StatelessWidget {
  const BottomOvalNav({super.key, required this.currentIndex, required this.onTap});

  /// Индекс текущей вкладки.
  final int currentIndex;

  /// Колбэк нажатия по кнопке вкладки.
  final ValueChanged<int> onTap;

  @override
  Widget build(BuildContext context) {
    final ovalW = MediaQuery.of(context).size.width * 0.7;
    return Center(
      child: Container(
        width: ovalW,
        height: Ui.ovalH + 10,
        decoration: BoxDecoration(
          borderRadius: Ui.br,
          gradient: const LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [kSurface, kSurface],
          ),
          border: Border.all(color: kBorder),
          boxShadow: const [BoxShadow(color: Color(0x4D000000), blurRadius: 14, offset: Offset(0, 6))],
        ),
        padding: Ui.pH,
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceEvenly,
          children: [
            _CircleNavButton(
              tooltip: 'Главная',
              svgAsset: 'assets/icons/home.svg',
              selected: currentIndex == 0,
              onTap: () => onTap(0),
            ),
            _CircleNavButton(
              tooltip: 'Профиль',
              svgAsset: 'assets/icons/profile.svg',
              selected: currentIndex == 1,
              onTap: () => onTap(1),
            ),
          ],
        ),
      ),
    );
  }
}

class _CircleNavButton extends StatefulWidget {
  const _CircleNavButton({
    this.icon,
    this.svgAsset,
    required this.onTap,
    required this.selected,
    required this.tooltip,
  }) : assert((icon != null) ^ (svgAsset != null), 'Передай ИЛИ icon, ИЛИ svgAsset');

  final IconData? icon;
  final String? svgAsset;
  final VoidCallback onTap;
  final bool selected;
  final String tooltip;

  @override
  State<_CircleNavButton> createState() => _CircleNavButtonState();
}

class _CircleNavButtonState extends State<_CircleNavButton> {
  bool _pressed = false;

  static const double iconScale = 1.0;

  @override
  Widget build(BuildContext context) {
    final outerBase = 70.0 * iconScale;
    final outerSel = 88.0 * iconScale;
    final innerBase = 36.0 * iconScale;
    final innerSel = 34.0 * iconScale;

    final outer = widget.selected ? outerSel : outerBase;
    final inner = widget.selected ? innerSel : innerBase;

    final fg = Colors.white;
    final bg = widget.selected ? const Color(0xFF383838) : kSurface;

    final selectedScale = widget.selected ? 1.12 : 1.0;
    final pressScale = _pressed ? 0.96 : 1.0;
    final totalScale = selectedScale * pressScale;

    final iconWidget = widget.svgAsset != null
        ? SvgPicture.asset(
      widget.svgAsset!,
      width: inner,
      height: inner,
      fit: BoxFit.contain,
      colorFilter: ColorFilter.mode(fg, BlendMode.srcIn),
    )
        : Icon(widget.icon!, size: inner, color: fg);

    return Tooltip(
      message: widget.tooltip,
      child: GestureDetector(
        onTapDown: (_) => setState(() => _pressed = true),
        onTapCancel: () => setState(() => _pressed = false),
        onTapUp: (_) => setState(() => _pressed = false),
        onTap: () {
          Feedback.forTap(context);
          widget.onTap();
        },
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 180),
          width: outer,
          height: outer,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            color: bg,
            border: Border.all(color: Colors.white, width: 1),
            boxShadow: widget.selected && !_pressed
                ? const [BoxShadow(color: Color(0x1AFFFFFF), blurRadius: 10, offset: Offset(0, 4))]
                : const [],
          ),
          child: AnimatedScale(
            duration: const Duration(milliseconds: 120),
            scale: totalScale,
            curve: Curves.easeOutQuad,
            child: Center(child: iconWidget),
          ),
        ),
      ),
    );
  }
}
