import 'package:flutter/material.dart';

import '../backend/backend.dart';
import '../backend/models.dart';

/// Paired devices: rename, revoke, ping over Reticulum.
class DevicesPage extends StatefulWidget {
  const DevicesPage({super.key, required this.backend});

  final Backend backend;

  @override
  State<DevicesPage> createState() => _DevicesPageState();
}

class _DevicesPageState extends State<DevicesPage> {
  List<Device> _devices = [];
  Status? _status;
  String _line = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final d = await widget.backend.devices();
      final s = await widget.backend.status();
      setState(() {
        _devices = d;
        _status = s;
      });
    } catch (e) {
      setState(() => _line = '$e');
    }
  }

  Future<void> _do(Future<Object?> Function() f, String ok) async {
    try {
      final r = await f();
      setState(() => _line = r is String && r.isNotEmpty ? r : ok);
    } catch (e) {
      setState(() => _line = '$e');
    }
    _load();
  }

  Future<void> _rename(Device d) async {
    final c = TextEditingController(text: d.name);
    final name = await showDialog<String>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Rename'),
        content: TextField(
          controller: c,
          autofocus: true,
          onSubmitted: (v) => Navigator.pop(context, v),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, c.text),
            child: const Text('Rename'),
          ),
        ],
      ),
    );
    if (name != null && name.trim().isNotEmpty) {
      _do(() => widget.backend.rename(d.fingerprint, name.trim()), 'Renamed');
    }
  }

  Future<void> _revoke(Device d) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Revoke ${d.name}?'),
        content: const Text(
          'It will no longer be able to send to or receive from this device.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Revoke'),
          ),
        ],
      ),
    );
    if (ok == true) _do(() => widget.backend.revoke(d.fingerprint), 'Revoked');
  }

  @override
  Widget build(BuildContext context) {
    final s = _status;
    return Scaffold(
      appBar: AppBar(title: const Text('Devices')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (s != null)
            Card(
              child: ListTile(
                leading: const Icon(Icons.smartphone),
                title: Text('This device: ${s.name}'),
                subtitle: Text(
                  [
                    s.fingerprint.length >= 16
                        ? s.fingerprint.substring(0, 16)
                        : s.fingerprint,
                    if (s.rnsAddr.isNotEmpty) 'Reticulum ${s.rnsAddr}',
                    if (s.rnsHubs.isNotEmpty) 'hubs: ${s.rnsHubs.join(', ')}',
                  ].join('\n'),
                ),
              ),
            ),
          if (_line.isNotEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(_line),
            ),
          if (_devices.isEmpty) const Text('No paired devices.'),
          for (final d in _devices)
            Card(
              child: ListTile(
                title: Text(d.name),
                subtitle: Text('${d.fingerprint.substring(0, 16)} · ${d.addr}'),
                trailing: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (s != null && s.rnsAddr.isNotEmpty)
                      IconButton(
                        tooltip: 'Ping over Reticulum',
                        icon: const Icon(Icons.network_ping),
                        onPressed: () =>
                            _do(() => widget.backend.ping(d.fingerprint), ''),
                      ),
                    IconButton(
                      tooltip: 'Rename',
                      icon: const Icon(Icons.edit),
                      onPressed: () => _rename(d),
                    ),
                    IconButton(
                      tooltip: 'Revoke',
                      icon: const Icon(Icons.delete),
                      onPressed: () => _revoke(d),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
