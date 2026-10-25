import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';

import 'backend.dart';
import 'models.dart';

/// The core embedded in the Android app (core/, gomobile), reached through
/// the AdropService in Kotlin.
class ChannelBackend implements Backend {
  static const _calls = MethodChannel('adrop/core');
  static const _events = EventChannel('adrop/events');

  Future<T> _call<T>(String method, [Object? args]) async {
    try {
      return await _calls.invokeMethod<T>(method, args) as T;
    } on PlatformException catch (e) {
      throw BackendException(e.message ?? e.code);
    }
  }

  Future<dynamic> _json(String method, [Object? args]) async =>
      jsonDecode(await _call<String>(method, args));

  @override
  Stream<AdropEvent> events() => _events.receiveBroadcastStream().map(
    (raw) =>
        AdropEvent.fromJson(jsonDecode(raw as String) as Map<String, dynamic>),
  );

  @override
  Future<Status> status() async => Status.fromJson(await _json('status'));

  @override
  Future<List<Device>> devices() async =>
      listOf(await _json('devices'), Device.fromJson);

  @override
  Future<List<NearbyDevice>> nearby() async =>
      listOf(await _json('nearby'), NearbyDevice.fromJson);

  @override
  Future<void> rename(String target, String name) =>
      _call('rename', {'target': target, 'name': name});

  @override
  Future<void> revoke(String target) => _call('revoke', target);

  @override
  PairSession showPairing() => _ChannelPairSession(this);

  @override
  Future<String> pairUri(String uri) async =>
      'paired with ${await _call<String>('pair', uri.trim())}';

  @override
  Future<String> pairWith(String target) async =>
      'paired with ${await _call<String>('pairWith', target)}';

  @override
  Future<List<PairInfo>> pendingPairs() async =>
      listOf(await _json('pendingPairs'), PairInfo.fromJson);

  @override
  Future<void> confirmPair(String id, bool ok) =>
      _call('confirmPair', {'id': id, 'ok': ok});

  @override
  Future<String> sendFiles(
    String target,
    List<String> paths,
    void Function(String) progress,
  ) async {
    final sub = events()
        .where((e) => e.kind == 'send-progress')
        .listen((e) => progress(e.file));
    try {
      return await _call<String>('sendFiles', {
        'target': target,
        'paths': jsonEncode(paths),
      });
    } finally {
      await sub.cancel();
    }
  }

  @override
  Future<String> sendText(String target, String? text) =>
      _call<String>('sendText', {'target': target, 'text': text ?? ''});

  @override
  Future<List<QueueItem>> queue() async =>
      listOf(await _json('queue'), QueueItem.fromJson);

  @override
  Future<void> unqueue(String id) => _call('unqueue', id);

  @override
  Future<String> ping(String target) async =>
      'pong in ${await _call<String>('ping', target)}';

  @override
  bool get canSendOwnClipboard => false;

  /// Settings kept by the app (Kotlin SharedPreferences).
  Future<bool> setting(String key) async =>
      await _call<bool>('getSetting', key);
  Future<void> setSetting(String key, bool value) =>
      _call('setSetting', {'key': key, 'value': value});

  /// Files shared into the app ("Поделиться"), copied to its cache.
  Future<List<String>> takeShared() async =>
      List<String>.from(await _call<List<Object?>>('takeShared'));

  /// Lets the user pick a folder and copies it into the app's cache, where
  /// the core can send it; null when cancelled.
  Future<String?> pickFolder() => _call<String?>('pickFolder');

  /// Opens a received file with the system viewer.
  Future<void> open(String path) => _call('open', path);
}

class _ChannelPairSession implements PairSession {
  _ChannelPairSession(ChannelBackend b) {
    () async {
      try {
        _lines.add(await b._call<String>('showPairing'));
        _sub = b.events().where((e) => e.kind == 'paired').listen((e) {
          _lines.add('paired with ${e.peer}');
        });
      } catch (e) {
        _lines.addError(e);
      }
    }();
  }

  final _lines = StreamController<String>();
  StreamSubscription<AdropEvent>? _sub;

  @override
  Stream<String> get lines => _lines.stream;

  @override
  void close() {
    _sub?.cancel();
    if (!_lines.isClosed) _lines.close();
  }
}
