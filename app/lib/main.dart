import 'package:flutter/material.dart';

import 'core.dart';

void main() =>
    runApp(const MaterialApp(title: 'adrop proto', home: ProtoPage()));

/// Step 1 of PLAN.md: pair with a PC daemon by its URI, then press Hello.
class ProtoPage extends StatefulWidget {
  const ProtoPage({super.key});

  @override
  State<ProtoPage> createState() => _ProtoPageState();
}

class _ProtoPageState extends State<ProtoPage> {
  final _core = Core();
  final _uri = TextEditingController();
  String _fingerprint = '';
  List<String> _peers = [];
  String? _peer;
  String _result = '';
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _run(() async {
      _fingerprint = await _core.start();
      await _loadPeers();
      return 'core started';
    });
  }

  Future<void> _loadPeers() async {
    _peers = await _core.peers();
    if (!_peers.contains(_peer)) _peer = _peers.isEmpty ? null : _peers.first;
  }

  Future<void> _run(Future<String> Function() action) async {
    setState(() => _busy = true);
    String result;
    try {
      result = await action();
    } catch (e) {
      result = 'error: $e';
    }
    if (!mounted) return;
    setState(() {
      _busy = false;
      _result = result;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('adrop proto')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          SelectableText('fingerprint: $_fingerprint'),
          const SizedBox(height: 16),
          TextField(
            controller: _uri,
            decoration: const InputDecoration(
              labelText: 'adrop://pair?d=… (adrop pair show)',
            ),
          ),
          const SizedBox(height: 8),
          FilledButton(
            onPressed: _busy
                ? null
                : () => _run(() async {
                    final name = await _core.pair(_uri.text);
                    await _loadPeers();
                    _peer = name;
                    return 'paired with $name';
                  }),
            child: const Text('Pair'),
          ),
          const SizedBox(height: 16),
          DropdownButton<String>(
            value: _peer,
            hint: const Text('no paired devices'),
            items: [
              for (final p in _peers)
                DropdownMenuItem(value: p, child: Text(p)),
            ],
            onChanged: (p) => setState(() => _peer = p),
          ),
          FilledButton(
            key: const Key('hello'),
            onPressed: _busy || _peer == null
                ? null
                : () => _run(
                    () async => 'hello from ${await _core.hello(_peer!)}',
                  ),
            child: const Text('Hello'),
          ),
          const SizedBox(height: 16),
          SelectableText(_result, key: const Key('result')),
        ],
      ),
    );
  }
}
