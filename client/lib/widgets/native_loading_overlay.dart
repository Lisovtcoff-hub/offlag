import 'package:flutter/material.dart';

import '../theme.dart';

class NativeLoadingOverlay extends StatelessWidget {
  const NativeLoadingOverlay({
    super.key,
    required this.label,
    this.barrierColor = const Color(0x8A000000),
  });

  final String label;
  final Color barrierColor;

  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: barrierColor,
      child: Center(
        child: DefaultTextStyle.merge(
          style: const TextStyle(
            color: Colors.white,
            decoration: TextDecoration.none,
          ),
          child: Container(
            margin: const EdgeInsets.symmetric(horizontal: 24),
            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 20),
            decoration: BoxDecoration(
              color: kSurface,
              borderRadius: BorderRadius.circular(kRadiusXXL),
              border: Border.all(color: kBorder),
            ),
            child: Row(
              children: [
                const SizedBox(
                  width: 24,
                  height: 24,
                  child: CircularProgressIndicator.adaptive(strokeWidth: 2.8),
                ),
                const SizedBox(width: 14),
                Expanded(
                  child: Text(
                    label,
                    style: const TextStyle(
                      fontSize: 15,
                      fontWeight: FontWeight.w700,
                      color: Colors.white,
                      decoration: TextDecoration.none,
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
