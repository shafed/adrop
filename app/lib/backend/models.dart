// Data the daemon (Linux, over IPC) or the embedded core (Android) reports,
// in the JSON of internal/ipc.

class Device {
  Device(this.name, this.fingerprint, this.addr);
  factory Device.fromJson(Map<String, dynamic> j) =>
      Device(j['name'] ?? '', j['fingerprint'] ?? '', j['addr'] ?? '');
  final String name;
  final String fingerprint;
  final String addr;
}

class NearbyDevice {
  NearbyDevice(this.name, this.addr, this.fingerprint, this.paired);
  factory NearbyDevice.fromJson(Map<String, dynamic> j) => NearbyDevice(
    j['name'] ?? '',
    j['addr'] ?? '',
    j['fingerprint'] ?? '',
    j['paired'] == true,
  );
  final String name;
  final String addr;
  final String fingerprint;
  final bool paired;
}

class PairInfo {
  PairInfo(this.id, this.name, this.fingerprint, this.code);
  factory PairInfo.fromJson(Map<String, dynamic> j) => PairInfo(
    j['id'] ?? '',
    j['name'] ?? '',
    j['fingerprint'] ?? '',
    j['code'] ?? '',
  );
  final String id;
  final String name;
  final String fingerprint;
  final String code;
}

class QueueItem {
  QueueItem(this.id, this.peer, this.what, this.reason, this.tries);
  factory QueueItem.fromJson(Map<String, dynamic> j) => QueueItem(
    j['id'] ?? '',
    j['peer'] ?? '',
    j['what'] ?? '',
    j['reason'] ?? '',
    j['tries'] ?? 0,
  );
  final String id;
  final String peer;
  final String what;
  final String reason;
  final int tries;
}

class Status {
  Status(this.name, this.fingerprint, this.rnsAddr, this.rnsHubs);
  factory Status.fromJson(Map<String, dynamic> j) => Status(
    j['name'] ?? '',
    j['fingerprint'] ?? '',
    j['rns_addr'] ?? '',
    List<String>.from(j['rns_hubs'] ?? const []),
  );
  final String name;
  final String fingerprint;
  final String rnsAddr;
  final List<String> rnsHubs;
}

/// A daemon event (ipc.Event): receives, pairing requests, the queue.
class AdropEvent {
  AdropEvent(
    this.kind,
    this.peer,
    this.file,
    this.path,
    this.index,
    this.count,
    this.bytesDone,
    this.total,
    this.err,
    this.pair,
  );
  factory AdropEvent.fromJson(Map<String, dynamic> j) => AdropEvent(
    j['kind'] ?? '',
    j['peer'] ?? '',
    j['file'] ?? '',
    j['path'] ?? '',
    j['index'] ?? 0,
    j['count'] ?? 0,
    j['bytes_done'] ?? 0,
    j['total'] ?? 0,
    j['err'] ?? '',
    j['pair'] == null ? null : PairInfo.fromJson(j['pair']),
  );
  final String kind;
  final String peer;
  final String file;
  final String path;
  final int index;
  final int count;
  final int bytesDone;
  final int total;
  final String err;
  final PairInfo? pair;
}

List<T> listOf<T>(dynamic raw, T Function(Map<String, dynamic>) f) =>
    raw == null
    ? <T>[]
    : (raw as List).map((e) => f(e as Map<String, dynamic>)).toList();
