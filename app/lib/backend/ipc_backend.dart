import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'backend.dart';
import 'models.dart';

/// The daemon's IPC (internal/ipc): newline-delimited JSON over the Unix
/// socket; a request, then responses until one has "done".
class IpcBackend implements Backend {
  static String get socketPath {
    final env = Platform.environment;
    return env['ADROP_SOCKET'] ??
        (env['XDG_RUNTIME_DIR'] != null
            ? '${env['XDG_RUNTIME_DIR']}/adrop.sock'
            : '/tmp/adrop.sock');
  }

  Future<Socket> _connect() async {
    try {
      return await Socket.connect(
        InternetAddress(socketPath, type: InternetAddressType.unix),
        0,
      );
    } on SocketException {
      throw BackendException('The adrop daemon is not running.');
    }
  }

  /// Streams the responses to one request; the stream ends after "done".
  Stream<Map<String, dynamic>> _stream(
    Map<String, dynamic> req, {
    Socket? keep,
  }) async* {
    final s = keep ?? await _connect();
    s.write('${jsonEncode(req)}\n');
    try {
      await for (final line
          in s
              .cast<List<int>>()
              .transform(utf8.decoder)
              .transform(const LineSplitter())) {
        final r = jsonDecode(line) as Map<String, dynamic>;
        if ((r['err'] ?? '') != '') throw BackendException(r['err']);
        yield r;
        if (r['done'] == true) break;
      }
    } finally {
      s.destroy();
    }
  }

  Future<Map<String, dynamic>> _call(Map<String, dynamic> req) async {
    Map<String, dynamic> last = {};
    await for (final r in _stream(req)) {
      last = r;
    }
    return last;
  }

  @override
  Stream<AdropEvent> events() async* {
    await for (final r in _stream({'cmd': 'subscribe'})) {
      if (r['event'] != null) yield AdropEvent.fromJson(r['event']);
    }
    throw BackendException('The daemon stopped.');
  }

  @override
  Future<Status> status() async =>
      Status.fromJson((await _call({'cmd': 'status'}))['status'] ?? {});

  @override
  Future<List<Device>> devices() async =>
      listOf((await _call({'cmd': 'devices'}))['devices'], Device.fromJson);

  @override
  Future<List<NearbyDevice>> nearby() async =>
      listOf((await _call({'cmd': 'nearby'}))['nearby'], NearbyDevice.fromJson);

  @override
  Future<void> rename(String target, String name) =>
      _call({'cmd': 'rename', 'target': target, 'name': name});

  @override
  Future<void> revoke(String target) =>
      _call({'cmd': 'revoke', 'target': target});

  @override
  PairSession showPairing() => _IpcPairSession(this);

  @override
  Future<String> pairUri(String uri) async =>
      (await _call({'cmd': 'pair-add', 'pair_uri': uri.trim()}))['line'] ?? '';

  @override
  Future<String> pairWith(String target) async {
    final r = await _call({'cmd': 'pair-with', 'target': target});
    return r['line'] ?? '';
  }

  @override
  Future<List<PairInfo>> pendingPairs() async => listOf(
    (await _call({'cmd': 'pair-pending'}))['pending'],
    PairInfo.fromJson,
  );

  @override
  Future<void> confirmPair(String id, bool ok) =>
      _call({'cmd': 'pair-confirm', 'id': id, 'ok': ok});

  @override
  Future<String> sendFiles(
    String target,
    List<String> paths,
    void Function(String) progress,
  ) async {
    String result = '';
    await for (final r in _stream({
      'cmd': 'send-files',
      'target': target,
      'files': paths,
    })) {
      final line = (r['line'] ?? '') as String;
      if (line.startsWith('queued: ')) {
        result = line.substring('queued: '.length);
      } else if (line.isNotEmpty) {
        progress(line);
      }
    }
    return result;
  }

  @override
  Future<String> sendText(String target, String? text) async =>
      (await _call({
        'cmd': 'send-clipboard',
        'target': target,
        'text': ?text,
      }))['line'] ??
      '';

  @override
  Future<List<QueueItem>> queue() async =>
      listOf((await _call({'cmd': 'queue'}))['queue'], QueueItem.fromJson);

  @override
  Future<void> unqueue(String id) => _call({'cmd': 'unqueue', 'id': id});

  @override
  Future<String> ping(String target) async =>
      (await _call({'cmd': 'ping', 'target': target}))['line'] ?? '';

  @override
  bool get canSendOwnClipboard => true;
}

/// pair-show holds the daemon's pairing window open while the connection is.
class _IpcPairSession implements PairSession {
  _IpcPairSession(IpcBackend b) {
    _sub = b
        ._stream({'cmd': 'pair-show'})
        .listen(
          (r) {
            final line = (r['line'] ?? '') as String;
            if (line.isNotEmpty) _lines.add(line);
          },
          onError: (Object e) => _lines.addError(e),
          onDone: _lines.close,
        );
  }

  final _lines = StreamController<String>();
  late final StreamSubscription<Map<String, dynamic>> _sub;

  @override
  Stream<String> get lines => _lines.stream;

  @override
  void close() {
    _sub.cancel();
    if (!_lines.isClosed) _lines.close();
  }
}
