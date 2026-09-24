import 'package:flutter_test/flutter_test.dart';
import 'package:n9n_web/models.dart';

void main() {
  test('graph JSON survives a copy and preserves credential references', () {
    final draft = WorkflowDraft(nodes: [
      WorkflowNode(id: 'trigger', type: 'manual_trigger', x: 30, y: 40),
      WorkflowNode(id: 'send', type: 'send_email', x: 200, y: 80, config: {'to': '{{input.email}}'}, credentialId: 'secret-id'),
    ], edges: [WorkflowEdge(id: 'edge', source: 'trigger', target: 'send')]);
    final copy = draft.copy();
    expect(copy.toJson(), draft.toJson());
    copy.nodes[1].config['to'] = 'changed';
    expect(draft.nodes[1].config['to'], '{{input.email}}');
  });

  test('connections reject a cycle and a second incoming edge', () {
    final draft = WorkflowDraft(nodes: [
      WorkflowNode(id: 't', type: 'manual_trigger', x: 0, y: 0),
      WorkflowNode(id: 'a', type: 'set_fields', x: 0, y: 0),
      WorkflowNode(id: 'b', type: 'set_fields', x: 0, y: 0),
    ], edges: [
      WorkflowEdge(id: 'e1', source: 't', target: 'a'),
      WorkflowEdge(id: 'e2', source: 'a', target: 'b'),
    ]);
    expect(validateConnection(draft, 'b', 'a', 'out'), contains('incoming'));
    draft.edges.removeLast();
    expect(validateConnection(draft, 'a', 't', 'out'), contains('Triggers'));
    draft.edges.clear();
    draft.edges.add(WorkflowEdge(id: 'e3', source: 'a', target: 'b'));
    expect(validateConnection(draft, 'b', 'a', 'out'), contains('cycle'));
  });
}
