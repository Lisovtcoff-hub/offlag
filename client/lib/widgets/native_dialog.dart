import 'package:flutter/material.dart';

import '../theme.dart';

class NativeDialogFrame extends StatelessWidget {
  const NativeDialogFrame({
    super.key,
    required this.title,
    this.content,
    this.actions = const <Widget>[],
    this.onClose,
    this.insetPadding = const EdgeInsets.symmetric(horizontal: 24, vertical: 24),
  });

  final Widget title;
  final Widget? content;
  final List<Widget> actions;
  final VoidCallback? onClose;
  final EdgeInsets insetPadding;

  @override
  Widget build(BuildContext context) {
    return Dialog(
      backgroundColor: kSurface,
      insetPadding: insetPadding,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(kRadiusXXL),
        side: const BorderSide(color: kBorder),
      ),
      child: Padding(
        padding: const EdgeInsets.all(18),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(child: title),
                if (onClose != null)
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: onClose,
                  ),
              ],
            ),
            if (content != null) ...[
              const SizedBox(height: 8),
              content!,
            ],
            if (actions.isNotEmpty) ...[
              const SizedBox(height: 14),
              Row(
                children: [
                  const Spacer(),
                  ...actions,
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }
}

