import 'package:flutter/material.dart';

import '../backend/backend.dart';
import '../backend/models.dart';

/// Sends waiting for a channel to their device, and why.
class QueuePage extends StatefulWidget {
  const QueuePage({super.key, required this.backend});

  final Backend backend;

  @override
  State<QueuePage> createState() => _QueuePageState();
}

class _QueuePageState extends State<QueuePage> {
  List<QueueItem> _items = [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final q = await widget.backend.queue();
    if (mounted) setState(() => _items = q);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Waiting to send')),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (_items.isEmpty) const Text('Nothing is waiting.'),
        for (final q in _items)
          Card(
            child: ListTile(
              title: Text('${q.what} to ${q.peer}'),
              subtitle: Text('${q.reason}\nTries: ${q.tries}'),
              isThreeLine: true,
              trailing: IconButton(
                tooltip: 'Drop',
                icon: const Icon(Icons.close),
                onPressed: () async {
                  await widget.backend.unqueue(q.id);
                  _load();
                },
              ),
            ),
          ),
      ],
    ),
  );
}
