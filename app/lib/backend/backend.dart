import 'dart:io';

import 'channel_backend.dart';
import 'ipc_backend.dart';
import 'models.dart';

/// What the UI needs from adrop. On Linux the window is a client of the
/// resident daemon (IpcBackend); on Android the core runs inside the app's
/// foreground service (ChannelBackend).
abstract class Backend {
  factory Backend() => Platform.isAndroid ? ChannelBackend() : IpcBackend();

  /// Daemon events: receives (with the landed file), pairing requests, the
  /// queue. Reconnects by itself; errors mean the daemon is down.
  Stream<AdropEvent> events();

  Future<Status> status();
  Future<List<Device>> devices();
  Future<List<NearbyDevice>> nearby();
  Future<void> rename(String target, String name);
  Future<void> revoke(String target);

  /// Shows this device's pairing QR for as long as the returned session is
  /// open; the stream yields the URI, then "paired with …" when someone pairs.
  PairSession showPairing();
  Future<String> pairUri(String uri);
  Future<String> pairWith(String target);
  Future<List<PairInfo>> pendingPairs();
  Future<void> confirmPair(String id, bool ok);

  /// Sends files and folders as one session; [progress] gets the daemon's
  /// progress lines. Returns a non-empty reason when the send was queued.
  Future<String> sendFiles(
    String target,
    List<String> paths,
    void Function(String) progress,
  );

  /// Sends text, or this device's clipboard when [text] is null (PC only).
  Future<String> sendText(String target, String? text);

  Future<List<QueueItem>> queue();
  Future<void> unqueue(String id);
  Future<String> ping(String target);

  /// Whether the device can read its own clipboard to send it (the PC can).
  bool get canSendOwnClipboard;
}

abstract class PairSession {
  Stream<String> get lines;
  void close();
}

class BackendException implements Exception {
  BackendException(this.message);
  final String message;
  @override
  String toString() => message;
}
