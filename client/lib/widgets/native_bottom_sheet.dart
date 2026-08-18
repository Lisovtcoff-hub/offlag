import 'package:flutter/material.dart';

import '../theme.dart';

Future<T?> showNativeBottomSheet<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  bool isScrollControlled = true,
  bool useSafeArea = true,
  bool showDragHandle = true,
  bool isDismissible = true,
  bool enableDrag = true,
  Color backgroundColor = kBg,
  ShapeBorder shape = const RoundedRectangleBorder(
    borderRadius: BorderRadius.vertical(top: Radius.circular(kRadiusXXL)),
  ),
}) {
  return showModalBottomSheet<T>(
    context: context,
    builder: builder,
    isScrollControlled: isScrollControlled,
    useSafeArea: useSafeArea,
    showDragHandle: showDragHandle,
    isDismissible: isDismissible,
    enableDrag: enableDrag,
    backgroundColor: backgroundColor,
    shape: shape,
  );
}

