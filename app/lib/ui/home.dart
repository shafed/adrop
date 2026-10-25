import 'dart:async';
import 'dart:io';

import 'package:desktop_drop/desktop_drop.dart';
import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';

import '../backend/backend.dart';
import '../backend/channel_backend.dart';
import '../backend/models.dart';
import 'devices.dart';
import 'pair.dart';
import 'queue.dart';
import 'settings.dart';

/// The one screen: where to send, what to send, what came in.
class HomePage extends StatefulWidget {
  const HomePage({super.key, required this.backend});

  final Backend backend;

  @override
  State<HomePage> createState() => _HomePageState();
}

class Received {
  Received(this.peer, this.name, this.path, this.at);
  final String peer;
  final String name;
  final String path;
  final DateTime at;
}

class _HomePageState extends State<HomePage> with WidgetsBindingObserver {
  Backend get b => widget.backend;

  List<Device> _devices = [];
  List<NearbyDevice> _nearby = [];
  List<QueueItem> _queue = [];
  String? _target;
  String? _daemonDown;

  // Outgoing batch.
  List<String> _staged = [];
  bool _sending = false;
  String _outLine = '';
  String? _outError;

  // Incoming session.
  String? _inTitle;
  double? _inProgress;
  final List<Received> _history = [];

  final _text = TextEditingController();
  StreamSubscription<AdropEvent>? _events;
  Timer? _poll;
  final Set<String> _answering = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _refresh();
    _subscribe();
    _poll = Timer.periodic(const Duration(seconds: 5), (_) => _refresh());
    HardwareKeyboard.instance.addHandler(_onKey);
    if (b is ChannelBackend) _takeShared();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    HardwareKeyboard.instance.removeHandler(_onKey);
    _events?.cancel();
    _poll?.cancel();
    _text.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _refresh();
      if (b is ChannelBackend) _takeShared();
    }
  }

  Future<void> _takeShared() async {
    final shared = await (b as ChannelBackend).takeShared();
    if (shared.isNotEmpty) _stage(shared, sendNow: _target != null);
  }

  void _subscribe() {
    _events?.cancel();
    _events = b.events().listen(
      _onEvent,
      onError: (Object e) {
        setState(() => _daemonDown = '$e');
        Future.delayed(const Duration(seconds: 3), _subscribe);
      },
      onDone: () => Future.delayed(const Duration(seconds: 3), _subscribe),
    );
  }

  Future<void> _refresh() async {
    try {
      final devices = await b.devices();
      final nearby = await b.nearby();
      final queue = await b.queue();
      if (!mounted) return;
      setState(() {
        _daemonDown = null;
        _devices = devices;
        _nearby = nearby.where((n) => !n.paired).toList();
        _queue = queue;
        if (_target == null || !devices.any((d) => d.name == _target)) {
          _target = devices.isEmpty ? null : devices.first.name;
        }
      });
    } catch (e) {
      if (mounted) setState(() => _daemonDown = '$e');
    }
  }

  void _onEvent(AdropEvent e) {
    switch (e.kind) {
      case 'recv-start':
        setState(() {
          _inTitle = 'Receiving ${e.count} file(s) from ${e.peer}';
          _inProgress = 0;
        });
      case 'recv-progress':
        if (e.total > 0) setState(() => _inProgress = e.bytesDone / e.total);
      case 'recv-file-done':
        setState(
          () => _history.insert(
            0,
            Received(e.peer, e.file, e.path, DateTime.now()),
          ),
        );
      case 'recv-done':
        setState(() {
          _inTitle = null;
          _inProgress = null;
          if (e.file == 'clipboard') {
            _history.insert(
              0,
              Received(e.peer, 'clipboard', '', DateTime.now()),
            );
          }
        });
      case 'recv-error':
        setState(() {
          _inTitle = 'Receiving from ${e.peer} failed: ${e.err}';
          _inProgress = null;
        });
      case 'pair-request':
        if (e.pair != null) _answerPairing(e.pair!);
      case 'pair-done' ||
          'pair-error' ||
          'paired' ||
          'queue-sent' ||
          'queue-error':
        _refresh();
    }
  }

  Future<void> _answerPairing(PairInfo p) async {
    if (!_answering.add(p.id)) return;
    final ok = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (_) => PairCodeDialog(info: p),
    );
    try {
      await b.confirmPair(p.id, ok ?? false);
      _toast(ok == true ? 'Paired with ${p.name}' : 'Pairing declined');
    } catch (e) {
      _toast('$e');
    }
    _answering.remove(p.id);
    _refresh();
  }

  void _toast(String s) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(s)));
    }
  }

  void _stage(List<String> paths, {bool sendNow = true}) {
    setState(() {
      _staged = paths;
      _outError = null;
      _outLine = '';
    });
    if (sendNow) _sendStaged();
  }

  Future<void> _sendStaged() async {
    final target = _target;
    if (target == null || _staged.isEmpty || _sending) return;
    setState(() {
      _sending = true;
      _outError = null;
      _outLine = 'Connecting to $target…';
    });
    try {
      final queued = await b.sendFiles(target, _staged, (line) {
        if (mounted) setState(() => _outLine = line);
      });
      setState(
        () => _outLine = queued.isEmpty
            ? 'Sent ${_staged.length} item(s) to $target'
            : 'Queued: $queued',
      );
      if (queued.isNotEmpty) _refresh();
    } catch (e) {
      setState(() => _outError = '$e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _chooseFiles() async {
    final files = await openFiles();
    if (files.isNotEmpty) _stage(files.map((f) => f.path).toList());
  }

  Future<void> _chooseFolder() async {
    // On Android the system picker gives a tree URI, not a path: the app
    // copies the folder into its cache and returns that.
    final dir = b is ChannelBackend
        ? await (b as ChannelBackend).pickFolder()
        : await getDirectoryPath();
    if (dir != null) _stage([dir]);
  }

  Future<void> _takePhoto() async {
    final photo = await ImagePicker().pickImage(source: ImageSource.camera);
    if (photo != null) _stage([photo.path]);
  }

  /// Ctrl+V anywhere but in a text field sends copied file paths, like the
  /// window this replaces.
  bool _onKey(KeyEvent e) {
    // Esc goes back from the pairing, devices and queue pages, as it closed
    // those dialogs in the window this replaces.
    if (e is KeyDownEvent &&
        e.logicalKey == LogicalKeyboardKey.escape &&
        ModalRoute.of(context)?.isCurrent != true) {
      Navigator.of(context).maybePop();
      return true;
    }
    if (e is! KeyDownEvent ||
        e.logicalKey != LogicalKeyboardKey.keyV ||
        !HardwareKeyboard.instance.isControlPressed) {
      return false;
    }
    final focused = FocusManager.instance.primaryFocus?.context?.widget;
    if (focused is EditableText || ModalRoute.of(context)?.isCurrent != true) {
      return false;
    }
    _pastePaths();
    return true;
  }

  /// Ctrl+V: a file path (or file:// URI) on the clipboard is sent at once.
  Future<void> _pastePaths() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final paths = <String>[];
    for (var line in (data?.text ?? '').split('\n')) {
      line = line.trim();
      if (line.startsWith('file://')) {
        try {
          line = Uri.parse(line).toFilePath();
        } on FormatException {
          continue;
        }
      }
      if (line.isNotEmpty &&
          FileSystemEntity.typeSync(line) != FileSystemEntityType.notFound) {
        paths.add(line);
      }
    }
    if (paths.isEmpty) {
      _toast('No file path on the clipboard');
      return;
    }
    _stage(paths);
  }

  Future<void> _sendText() async {
    final target = _target;
    if (target == null) return;
    try {
      final how = await b.sendText(
        target,
        b.canSendOwnClipboard && _text.text.isEmpty ? null : _text.text,
      );
      _toast(how);
      _text.clear();
      _refresh();
    } catch (e) {
      _toast('$e');
    }
  }

  Future<void> _pairWith(NearbyDevice n) async {
    _toast('Pairing with ${n.name}: compare the codes on both screens');
    try {
      _toast(await b.pairWith(n.name));
    } catch (e) {
      _toast('$e');
    }
    _refresh();
  }

  Future<void> _startDaemon() async {
    final r = Platform.isWindows
        ? await Process.run('adrop-daemon.exe', ['daemon'], runInShell: true)
        : await Process.run('systemctl', ['--user', 'start', 'adrop']);
    if (r.exitCode != 0) _toast('systemctl: ${r.stderr}');
    await Future.delayed(const Duration(seconds: 1));
    _refresh();
    _subscribe();
  }

  @override
  Widget build(BuildContext context) {
    final desktop = !Platform.isAndroid;
    Widget body = ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (_daemonDown != null) _daemonCard(),
        if (_inTitle != null) _receiveCard(),
        _section('Send to'),
        if (_devices.isEmpty)
          const Text(
            'No paired devices yet. Pair one below or with the QR button above.',
          ),
        for (final d in _devices)
          Card(
            child: ListTile(
              leading: Icon(
                Icons.devices,
                color: d.name == _target
                    ? Theme.of(context).colorScheme.primary
                    : null,
              ),
              title: Text(d.name),
              subtitle: Text(d.addr),
              selected: d.name == _target,
              trailing: d.name == _target
                  ? const Icon(Icons.check_circle)
                  : null,
              onTap: () => setState(() => _target = d.name),
            ),
          ),
        if (_nearby.isNotEmpty) ...[
          _section('Nearby'),
          for (final n in _nearby)
            Card(
              child: ListTile(
                leading: const Icon(Icons.wifi_tethering),
                title: Text(n.name),
                subtitle: Text(n.addr),
                trailing: FilledButton.tonal(
                  onPressed: () => _pairWith(n),
                  child: const Text('Pair'),
                ),
              ),
            ),
        ],
        _section('Files'),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            FilledButton.icon(
              onPressed: _target == null || _sending ? null : _chooseFiles,
              icon: const Icon(Icons.attach_file),
              label: const Text('Choose files'),
            ),
            OutlinedButton.icon(
              onPressed: _target == null || _sending ? null : _chooseFolder,
              icon: const Icon(Icons.folder),
              label: const Text('Folder'),
            ),
            if (!desktop)
              OutlinedButton.icon(
                onPressed: _target == null || _sending ? null : _takePhoto,
                icon: const Icon(Icons.photo_camera),
                label: const Text('Photo'),
              ),
          ],
        ),
        if (desktop) _dropZone(),
        if (_staged.isNotEmpty || _outLine.isNotEmpty || _outError != null)
          _outCard(),
        _section('Clipboard'),
        TextField(
          controller: _text,
          minLines: 1,
          maxLines: 4,
          decoration: InputDecoration(
            hintText: b.canSendOwnClipboard
                ? 'Text to send (empty: this PC\'s clipboard)'
                : 'Text to send',
            border: const OutlineInputBorder(),
          ),
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: FilledButton.icon(
            onPressed: _target == null ? null : _sendText,
            icon: const Icon(Icons.content_paste_go),
            label: Text(b.canSendOwnClipboard ? 'Send clipboard' : 'Send text'),
          ),
        ),
        if (_history.isNotEmpty) ...[
          _section('Received'),
          for (final r in _history)
            ListTile(
              dense: true,
              leading: Icon(
                r.path.isEmpty ? Icons.content_paste : Icons.insert_drive_file,
              ),
              title: Text(r.name),
              subtitle: Text(
                'from ${r.peer} · ${TimeOfDay.fromDateTime(r.at).format(context)}',
              ),
              onTap: r.path.isEmpty ? null : () => _open(r.path),
            ),
        ],
      ],
    );
    return Scaffold(
      appBar: AppBar(
        title: const Text('adrop'),
        actions: [
          if (_queue.isNotEmpty)
            IconButton(
              tooltip: 'Waiting to send',
              icon: Badge(
                label: Text('${_queue.length}'),
                child: const Icon(Icons.schedule_send),
              ),
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(builder: (_) => QueuePage(backend: b)),
              ).then((_) => _refresh()),
            ),
          IconButton(
            tooltip: 'Pair a device',
            icon: const Icon(Icons.qr_code),
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => PairPage(backend: b)),
            ).then((_) => _refresh()),
          ),
          IconButton(
            tooltip: 'Devices',
            icon: const Icon(Icons.devices_other),
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => DevicesPage(backend: b)),
            ).then((_) => _refresh()),
          ),
          if (b is ChannelBackend)
            IconButton(
              tooltip: 'Settings',
              icon: const Icon(Icons.settings),
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => SettingsPage(backend: b as ChannelBackend),
                ),
              ),
            ),
        ],
      ),
      body: body,
    );
  }

  Future<void> _open(String path) async {
    if (b is ChannelBackend) {
      await (b as ChannelBackend).open(path);
    } else {
      await Process.run(Platform.isWindows ? 'explorer' : 'xdg-open', [path]);
    }
  }

  Widget _section(String title) => Padding(
    padding: const EdgeInsets.only(top: 20, bottom: 8),
    child: Text(title, style: Theme.of(context).textTheme.titleSmall),
  );

  Widget _daemonCard() => Card(
    color: Theme.of(context).colorScheme.errorContainer,
    child: ListTile(
      leading: const Icon(Icons.power_off),
      title: Text(_daemonDown!),
      trailing: Platform.isAndroid
          ? null
          : FilledButton(
              onPressed: _startDaemon,
              child: const Text('Start daemon'),
            ),
    ),
  );

  Widget _receiveCard() => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(_inTitle!),
          if (_inProgress != null) ...[
            const SizedBox(height: 8),
            LinearProgressIndicator(value: _inProgress),
          ],
        ],
      ),
    ),
  );

  Widget _dropZone() => Padding(
    padding: const EdgeInsets.only(top: 12),
    child: DropTarget(
      onDragDone: (d) {
        if (_target != null && !_sending) {
          _stage(d.files.map((f) => f.path).toList());
        }
      },
      child: Container(
        height: 72,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          border: Border.all(
            color: Theme.of(context).colorScheme.outlineVariant,
          ),
          borderRadius: BorderRadius.circular(12),
        ),
        child: const Text('Drop files here · Ctrl+V sends a copied path'),
      ),
    ),
  );

  Widget _outCard() => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final p in _staged.take(5))
            Text(p.split('/').last, overflow: TextOverflow.ellipsis),
          if (_staged.length > 5) Text('and ${_staged.length - 5} more'),
          if (_sending)
            const Padding(
              padding: EdgeInsets.only(top: 8),
              child: LinearProgressIndicator(),
            ),
          if (_outLine.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(_outLine),
            ),
          if (_outError != null) ...[
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                _outError!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
            TextButton.icon(
              onPressed: _sendStaged,
              icon: const Icon(Icons.refresh),
              label: const Text('Retry'),
            ),
          ],
        ],
      ),
    ),
  );
}
