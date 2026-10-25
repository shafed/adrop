import 'package:flutter/material.dart';

import 'backend/backend.dart';
import 'ui/home.dart';

void main() => runApp(AdropApp(backend: Backend()));

class AdropApp extends StatelessWidget {
  const AdropApp({super.key, required this.backend});

  final Backend backend;

  // The icon's colors: indigo and orange.
  static const _seed = Color(0xFF2B3A8F);

  @override
  Widget build(BuildContext context) {
    ThemeData theme(Brightness b) => ThemeData(
      colorScheme: ColorScheme.fromSeed(seedColor: _seed, brightness: b),
      useMaterial3: true,
    );
    return MaterialApp(
      title: 'adrop',
      theme: theme(Brightness.light),
      darkTheme: theme(Brightness.dark),
      home: HomePage(backend: backend),
    );
  }
}
