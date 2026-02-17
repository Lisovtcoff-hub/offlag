import 'package:flutter/material.dart';

import '../theme.dart';

EdgeInsets nativeSheetPadding(
  BuildContext context, {
  double top = 16,
  double horizontal = 20,
  double bottom = 20,
}) {
  return EdgeInsets.only(
    left: horizontal,
    right: horizontal,
    top: top,
    bottom: bottom + MediaQuery.of(context).viewInsets.bottom,
  );
}

InputDecoration nativeSheetInputDecoration({
  required String hintText,
  String? errorText,
  String? counterText,
}) {
  return InputDecoration(
    hintText: hintText,
    hintStyle: const TextStyle(color: Colors.white),
    errorStyle: const TextStyle(color: Colors.white),
    errorText: errorText,
    counterText: counterText,
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
  );
}

Widget nativeSheetTitle(BuildContext context, String title) {
  return Center(
    child: Text(
      title,
      style: Theme.of(context).textTheme.titleLarge,
      textAlign: TextAlign.center,
    ),
  );
}
