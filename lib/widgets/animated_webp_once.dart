import 'dart:async';
import 'package:flutter/material.dart';

/// Проигрывает анимированный WebP и по истечении [duration]
/// накрывает его последним кадром [poster] (ручная «заморозка»).
class AnimatedWebpOnce extends StatefulWidget {
  const AnimatedWebpOnce({
    super.key,
    required this.asset,
    required this.poster,
    required this.duration,
    this.width,
    this.height,
    this.fit = BoxFit.contain,
    this.borderRadius = 0,
    this.buffer = const Duration(milliseconds: 100),
  });

  /// Путь к анимированному `.webp`.
  final String asset;

  /// Путь к PNG (последний кадр), которым перекрывается анимация.
  final String poster;

  /// Длительность самой анимации (без учёта [buffer]).
  final Duration duration;

  /// Запас по времени на декодирование перед показом [poster].
  final Duration buffer;

  /// Ширина виджета.
  final double? width;

  /// Высота виджета.
  final double? height;

  /// Режим вписывания изображения.
  final BoxFit fit;

  /// Радиус скругления, если нужно обрезать контент.
  final double borderRadius;

  @override
  State<AnimatedWebpOnce> createState() => _AnimatedWebpOnceState();
}

class _AnimatedWebpOnceState extends State<AnimatedWebpOnce> {
  bool _showPoster = false;
  Timer? _t;

  @override
  void initState() {
    super.initState();
    _t = Timer(widget.duration + widget.buffer, () {
      if (mounted) setState(() => _showPoster = true);
    });
  }

  @override
  void dispose() {
    _t?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final image = Image.asset(
      widget.asset,
      width: widget.width,
      height: widget.height,
      fit: widget.fit,
      gaplessPlayback: true,
    );

    final poster = Image.asset(
      widget.poster,
      width: widget.width,
      height: widget.height,
      fit: widget.fit,
    );

    Widget child = Stack(
      fit: StackFit.passthrough,
      children: [
        image,
        if (_showPoster) poster,
      ],
    );

    if (widget.borderRadius != 0) {
      child = ClipRRect(
        borderRadius: BorderRadius.circular(widget.borderRadius),
        child: child,
      );
    }
    return SizedBox(width: widget.width, height: widget.height, child: child);
  }
}
