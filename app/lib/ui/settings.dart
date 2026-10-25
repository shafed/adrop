import 'package:flutter/material.dart';

import '../backend/channel_backend.dart';

/// Phone settings. The features of PLAN.md steps 2–4 start switched off
/// until they have run on real devices.
class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key, required this.backend});

  final ChannelBackend backend;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  static const _keys = {
    'background': (
      'Receive when the app is closed',
      'Keeps a foreground service running, also after a reboot.',
    ),
    'tapPair': (
      'Pair by code',
      'Devices on the same Wi-Fi can pair by tapping and comparing a 6-digit code.',
    ),
    'reticulum': (
      'Outside this network',
      'Reach paired devices in other networks through public Reticulum hubs.',
    ),
  };
  final Map<String, bool> _values = {};

  @override
  void initState() {
    super.initState();
    for (final k in _keys.keys) {
      widget.backend.setting(k).then((v) => setState(() => _values[k] = v));
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Settings')),
    body: ListView(
      children: [
        for (final e in _keys.entries)
          SwitchListTile(
            title: Text(e.value.$1),
            subtitle: Text(e.value.$2),
            value: _values[e.key] ?? false,
            onChanged: (v) async {
              await widget.backend.setSetting(e.key, v);
              setState(() => _values[e.key] = v);
            },
          ),
        const Padding(
          padding: EdgeInsets.all(16),
          child: Text('Changes to pairing and Reticulum restart the service.'),
        ),
      ],
    ),
  );
}
