import 'dart:convert';

typedef Json = Map<String, dynamic>;

Json asJson(Object? value) => value is Map ? Map<String, dynamic>.from(value) : <String, dynamic>{};
List<Json> asJsonList(Object? value) => value is List ? value.map(asJson).toList() : <Json>[];

class WorkflowNode {
  WorkflowNode({required this.id, required this.type, required this.x, required this.y, Json? config, this.credentialId}) : config = config ?? {};
  String id;
  String type;
  double x;
  double y;
  Json config;
  String? credentialId;

  factory WorkflowNode.fromJson(Json data) {
    final position = asJson(data['position']);
    return WorkflowNode(
      id: '${data['id'] ?? ''}', type: '${data['type'] ?? ''}',
      x: (position['x'] as num?)?.toDouble() ?? 0,
      y: (position['y'] as num?)?.toDouble() ?? 0,
      config: asJson(data['config']), credentialId: data['credential_id']?.toString(),
    );
  }

  Json toJson() => {
    'id': id, 'type': type, 'position': {'x': x, 'y': y}, 'config': config,
    if (credentialId != null && credentialId!.isNotEmpty) 'credential_id': credentialId,
  };
}

class WorkflowEdge {
  WorkflowEdge({required this.id, required this.source, required this.target, this.sourcePort = 'out'});
  String id;
  String source;
  String target;
  String sourcePort;
  factory WorkflowEdge.fromJson(Json data) => WorkflowEdge(
    id: '${data['id'] ?? ''}', source: '${data['source'] ?? ''}',
    target: '${data['target'] ?? ''}', sourcePort: '${data['source_port'] ?? 'out'}',
  );
  Json toJson() => {'id': id, 'source': source, 'target': target, 'source_port': sourcePort};
}

class WorkflowDraft {
  WorkflowDraft({List<WorkflowNode>? nodes, List<WorkflowEdge>? edges})
      : nodes = nodes ?? [], edges = edges ?? [];
  List<WorkflowNode> nodes;
  List<WorkflowEdge> edges;
  factory WorkflowDraft.fromJson(Json data) => WorkflowDraft(
    nodes: asJsonList(data['nodes']).map(WorkflowNode.fromJson).toList(),
    edges: asJsonList(data['edges']).map(WorkflowEdge.fromJson).toList(),
  );
  Json toJson() => {'nodes': nodes.map((e) => e.toJson()).toList(), 'edges': edges.map((e) => e.toJson()).toList()};
  WorkflowDraft copy() => WorkflowDraft.fromJson(jsonDecode(jsonEncode(toJson())) as Json);
}

class Workflow {
  Workflow({required this.id, required this.name, required this.draft, required this.active, this.publishedVersion, this.updatedAt});
  String id;
  String name;
  WorkflowDraft draft;
  bool active;
  int? publishedVersion;
  String? updatedAt;
  factory Workflow.fromJson(Json data) => Workflow(
    id: '${data['id'] ?? ''}', name: '${data['name'] ?? 'Untitled workflow'}',
    draft: WorkflowDraft.fromJson(asJson(data['draft'])),
    active: data['active'] == true,
    publishedVersion: (data['published_version'] as num?)?.toInt(),
    updatedAt: data['updated_at']?.toString(),
  );
}

const triggerTypes = {'manual_trigger', 'webhook_trigger', 'schedule_trigger', 'email_trigger'};
const credentialTypes = {'email_trigger', 'send_email'};

String nodeLabel(String type) => switch (type) {
  'manual_trigger' => 'Manual trigger',
  'webhook_trigger' => 'Webhook',
  'schedule_trigger' => 'Schedule',
  'email_trigger' => 'Email trigger',
  'set_fields' => 'Set fields',
  'http_request' => 'HTTP request',
  'condition' => 'Condition',
  'send_email' => 'Send email',
  _ => type.replaceAll('_', ' '),
};

Json defaultConfig(String type) => switch (type) {
  'webhook_trigger' => {'secret': ''},
  'schedule_trigger' => {'interval_seconds': 300, 'timezone': 'UTC'},
  'email_trigger' => {'poll_seconds': 60, 'query': 'is:unread'},
  'set_fields' => {'fields': <String, dynamic>{}},
  'http_request' => {'url': '', 'method': 'GET', 'headers': <String, dynamic>{}, 'body': ''},
  'condition' => {'left': '', 'operator': 'equals', 'right': ''},
  'send_email' => {'to': '', 'subject': '', 'body': ''},
  _ => {},
};

String? validateConnection(WorkflowDraft draft, String source, String target, String port) {
  if (source == target) return 'A node cannot connect to itself.';
  final destination = draft.nodes.where((n) => n.id == target).firstOrNull;
  if (destination == null) return 'Target node was removed.';
  if (triggerTypes.contains(destination.type)) return 'Triggers cannot have incoming connections.';
  if (draft.edges.any((e) => e.target == target)) return 'Each action accepts one incoming connection.';
  if (draft.edges.any((e) => e.source == source && e.target == target && e.sourcePort == port)) return 'Connection already exists.';
  bool reaches(String at, String wanted, Set<String> visited) {
    if (at == wanted) return true;
    if (!visited.add(at)) return false;
    for (final edge in draft.edges.where((e) => e.source == at)) {
      if (reaches(edge.target, wanted, visited)) return true;
    }
    return false;
  }
  if (reaches(target, source, {})) return 'This connection would create a cycle.';
  return null;
}
