import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../backend/backend.dart';
import '../backend/models.dart';

/// Pairing by QR: this device's code to scan, and the other way round
/// (camera on the phone, a pasted URI anywhere).
class PairPage extends StatefulWidget {
  const PairPage({super.key, required this.backend});

  final Backend backend;

  @override
  State<PairPage> createState() => _PairPageState();
}

class _PairPageState extends State<PairPage> {
  late final PairSession _session;
  StreamSubscription<String>? _sub;
  String? _uri;
  String _status = 'Preparing the pairing code…';
  final _paste = TextEditingController();

  @override
  void initState() {
    super.initState();
    _session = widget.backend.showPairing();
    _sub = _session.lines.listen((line) {
      setState(() {
        if (line.startsWith('adrop://pair?d=')) {
          _uri = line;
          _status = 'Scan this QR with the device you want to pair.';
        } else {
          _status = line;
        }
      });
    }, onError: (Object e) => setState(() => _status = '$e'));
  }

  @override
  void dispose() {
    _sub?.cancel();
    _session.close();
    _paste.dispose();
    super.dispose();
  }

  Future<void> _pair(String uri) async {
    setState(() => _status = 'Pairing…');
    try {
      final line = await widget.backend.pairUri(uri);
      setState(() => _status = line);
    } catch (e) {
      setState(() => _status = '$e');
    }
  }

  Future<void> _scan() async {
    final uri = await Navigator.push<String>(
      context,
      MaterialPageRoute(builder: (_) => const _ScanPage()),
    );
    if (uri != null) _pair(uri);
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Scaffold(
      appBar: AppBar(title: const Text('Pair a device')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text(_status),
          const SizedBox(height: 16),
          Center(
            child: _uri == null
                ? const CircularProgressIndicator()
                : Container(
                    color: Colors.white,
                    padding: const EdgeInsets.all(12),
                    child: QrImageView(
                      data: _uri!,
                      size: 280,
                      backgroundColor: Colors.white,
                    ),
                  ),
          ),
          const SizedBox(height: 8),
          Center(
            child: OutlinedButton.icon(
              onPressed: _uri == null
                  ? null
                  : () {
                      Clipboard.setData(ClipboardData(text: _uri!));
                      setState(() => _status = 'Pairing URI copied.');
                    },
              icon: const Icon(Icons.copy),
              label: const Text('Copy URI'),
            ),
          ),
          const Divider(height: 40),
          Text(
            'Or pair with another device\'s code',
            style: Theme.of(context).textTheme.titleSmall,
          ),
          const SizedBox(height: 8),
          if (Platform.isAndroid)
            FilledButton.icon(
              onPressed: _scan,
              icon: const Icon(Icons.qr_code_scanner),
              label: const Text('Scan QR'),
            ),
          const SizedBox(height: 8),
          TextField(
            controller: _paste,
            decoration: InputDecoration(
              hintText: 'adrop://pair?d=…',
              border: const OutlineInputBorder(),
              suffixIcon: IconButton(
                icon: const Icon(Icons.link),
                onPressed: () => _pair(_paste.text),
              ),
            ),
            onSubmitted: _pair,
          ),
          const SizedBox(height: 16),
          Text(
            'Devices on the same network also appear under "Nearby" on the main screen: '
            'tap Pair there and compare the 6-digit codes.',
            style: TextStyle(color: scheme.onSurfaceVariant),
          ),
        ],
      ),
    );
  }
}

class _ScanPage extends StatefulWidget {
  const _ScanPage();

  @override
  State<_ScanPage> createState() => _ScanPageState();
}

class _ScanPageState extends State<_ScanPage> {
  bool _done = false;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Scan the pairing QR')),
    body: MobileScanner(
      onDetect: (capture) {
        for (final code in capture.barcodes) {
          final v = code.rawValue ?? '';
          if (!_done && v.startsWith('adrop://pair?d=')) {
            _done = true;
            Navigator.pop(context, v);
          }
        }
      },
    ),
  );
}

/// Tap pairing: both screens show the same 6 digits unless someone is in the
/// middle. The user confirms on each device.
class PairCodeDialog extends StatelessWidget {
  const PairCodeDialog({super.key, required this.info});

  final PairInfo info;

  @override
  Widget build(BuildContext context) {
    final code = '${info.code.substring(0, 3)} ${info.code.substring(3)}';
    return AlertDialog(
      title: Text('Pair with ${info.name}?'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text('Check that the other screen shows the same code:'),
          const SizedBox(height: 16),
          SelectableText(code, style: Theme.of(context).textTheme.displaySmall),
          const SizedBox(height: 8),
          Text(
            info.fingerprint.substring(0, 16),
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(context, true),
          child: const Text('Codes match'),
        ),
      ],
    );
  }
}
