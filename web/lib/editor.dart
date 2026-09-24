import 'dart:convert';
import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'api.dart';
import 'models.dart';
import 'main.dart';

class EditorScreen extends StatefulWidget {
  const EditorScreen({super.key, required this.api, required this.workflowId, required this.onNavigate});
  final Api api; final String workflowId; final ValueChanged<String> onNavigate;
  @override State<EditorScreen> createState() => _EditorScreenState();
}

class _EditorScreenState extends State<EditorScreen> {
  Workflow? workflow;
  List<Json> catalog = [], credentials = [], runs = [];
  String? error, selectedId, pendingSource, pendingPort;
  bool busy = false, dirty = false;
  int configRevision = 0;
  String paletteSearch = '';
  final undo = <WorkflowDraft>[], redo = <WorkflowDraft>[];
  final transform = TransformationController();
  int sequence = 0;

  @override void initState() { super.initState(); load(); }
  @override void dispose() { transform.dispose(); super.dispose(); }
  Future<void> load() async {
    try {
      final results = await Future.wait([widget.api.workflow(widget.workflowId), widget.api.nodes(), widget.api.credentials(), widget.api.runs(workflowId: widget.workflowId)]);
      if (mounted) setState(() { workflow = results[0] as Workflow; catalog = results[1] as List<Json>; credentials = results[2] as List<Json>; runs = results[3] as List<Json>; error = null; dirty = false; });
    } catch (e) { if (mounted) setState(() => error = '$e'); }
  }
  WorkflowNode? get selected { for (final node in workflow?.draft.nodes ?? <WorkflowNode>[]) { if (node.id == selectedId) return node; } return null; }
  void edit(void Function(WorkflowDraft) change) {
    final current = workflow;
    if (current == null) return;
    setState(() { undo.add(current.draft.copy()); if (undo.length > 80) undo.removeAt(0); redo.clear(); change(current.draft); dirty = true; });
  }
  void snapshot() { final current = workflow; if (current == null) return; undo.add(current.draft.copy()); if (undo.length > 80) undo.removeAt(0); redo.clear(); }
  void undoEdit() { if (undo.isEmpty || workflow == null) return; setState(() { redo.add(workflow!.draft.copy()); workflow!.draft = undo.removeLast(); dirty = true; configRevision++; }); }
  void redoEdit() { if (redo.isEmpty || workflow == null) return; setState(() { undo.add(workflow!.draft.copy()); workflow!.draft = redo.removeLast(); dirty = true; configRevision++; }); }
  String nextId(String prefix) => '${prefix}_${DateTime.now().microsecondsSinceEpoch}_${sequence++}';
  void addNode(String type, Offset scene) {
    if (triggerTypes.contains(type) && workflow!.draft.nodes.any((n) => triggerTypes.contains(n.type))) { showError(context, 'A workflow can have one trigger.'); return; }
    edit((draft) => draft.nodes.add(WorkflowNode(id: nextId('node'), type: type, x: scene.dx.clamp(15, 2180).toDouble(), y: scene.dy.clamp(15, 1450).toDouble(), config: defaultConfig(type))));
    setState(() => selectedId = workflow!.draft.nodes.last.id);
  }
  void connect(String target) {
    if (pendingSource == null) { setState(() => selectedId = target); return; }
    final problem = validateConnection(workflow!.draft, pendingSource!, target, pendingPort!);
    if (problem != null) { showError(context, problem); return; }
    edit((draft) => draft.edges.add(WorkflowEdge(id: nextId('edge'), source: pendingSource!, target: target, sourcePort: pendingPort!)));
    setState(() { pendingSource = null; pendingPort = null; });
  }
  void removeNode(WorkflowNode node) { edit((draft) { draft.nodes.removeWhere((n) => n.id == node.id); draft.edges.removeWhere((e) => e.source == node.id || e.target == node.id); }); setState(() => selectedId = null); }
  Future<void> save() async {
    if (workflow == null || busy) return;
    setState(() => busy = true);
    try { final saved = await widget.api.saveWorkflow(workflow!); if (mounted) setState(() { workflow = saved; dirty = false; }); if (mounted) showSuccess(context, 'Draft saved'); }
    catch (e) { if (mounted) showError(context, '$e'); }
    finally { if (mounted) setState(() => busy = false); }
  }
  Future<void> publish() async {
    if (workflow == null || busy) return;
    setState(() => busy = true);
    try {
      if (dirty) { workflow = await widget.api.saveWorkflow(workflow!); dirty = false; }
      final updated = await widget.api.publish(workflow!.id);
      if (mounted) { setState(() => workflow = updated); showSuccess(context, 'Version ${updated.publishedVersion} published'); }
    } catch (e) { if (mounted) showError(context, '$e'); }
    finally { if (mounted) setState(() => busy = false); }
  }
  Future<void> setActivation(bool value) async {
    if (workflow == null || busy) return;
    setState(() => busy = true);
    try { final updated = await widget.api.activate(workflow!.id, value); if (mounted) { setState(() => workflow = updated); showSuccess(context, value ? 'Workflow activated' : 'Workflow paused'); } }
    catch (e) { if (mounted) showError(context, '$e'); }
    finally { if (mounted) setState(() => busy = false); }
  }
  Future<void> runNow() async {
    if (workflow == null) return;
    final input = TextEditingController(text: '{}');
    String? localError;
    final data = await showDialog<Json>(context: context, builder: (context) => StatefulBuilder(builder: (context, setDialog) => AlertDialog(title: const Text('Run workflow'), content: SizedBox(width: 450, child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [const Text('Input JSON available to nodes as {{input.FIELD}}.', style: TextStyle(color: Color(0xff66716b))), const SizedBox(height: 16), TextField(controller: input, maxLines: 8, style: const TextStyle(fontFamily: 'monospace'), decoration: const InputDecoration(labelText: 'Input JSON')), if (localError != null) Text(localError!, style: const TextStyle(color: Colors.red))])), actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')), FilledButton(onPressed: () { try { final value = jsonDecode(input.text); if (value is! Map) throw const FormatException('Enter a JSON object.'); Navigator.pop(context, Map<String, dynamic>.from(value)); } catch (e) { setDialog(() => localError = 'Enter a valid JSON object.'); } }, child: const Text('Start run'))])));
    disposeDialogController(input); if (data == null) return;
    try { final run = await widget.api.run(workflow!.id, data); if (mounted) widget.onNavigate('/runs/${run['id']}'); }
    catch (e) { if (mounted) showError(context, '$e'); }
  }
  Future<void> editName() async {
    final controller = TextEditingController(text: workflow!.name);
    final name = await showDialog<String>(context: context, builder: (context) => AlertDialog(title: const Text('Rename workflow'), content: TextField(controller: controller, autofocus: true, onSubmitted: (v) => Navigator.pop(context, v), decoration: const InputDecoration(labelText: 'Name')), actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')), FilledButton(onPressed: () => Navigator.pop(context, controller.text), child: const Text('Rename'))]));
    disposeDialogController(controller); if (name != null && name.trim().isNotEmpty) setState(() { workflow!.name = name.trim(); dirty = true; });
  }
  @override Widget build(BuildContext context) {
    if (error != null) return Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 500), child: ErrorCard(error!, load)));
    if (workflow == null) return const Center(child: CircularProgressIndicator());
    final width = MediaQuery.sizeOf(context).width;
    return Column(children: [
      Container(color: Colors.white, padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 11), child: Wrap(spacing: 10, runSpacing: 10, crossAxisAlignment: WrapCrossAlignment.center, children: [
        IconButton(onPressed: () => widget.onNavigate('/workflows'), icon: const Icon(Icons.arrow_back), tooltip: 'All workflows'),
        InkWell(onTap: editName, child: Row(mainAxisSize: MainAxisSize.min, children: [Text(workflow!.name, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w800)), const SizedBox(width: 5), const Icon(Icons.edit_outlined, size: 15)])),
        if (dirty) const Text('Unsaved changes', style: TextStyle(color: Color(0xff9a6140), fontSize: 12)),
        StatusPill(workflow!.active ? 'Active' : workflow!.publishedVersion != null ? 'Published v${workflow!.publishedVersion}' : 'Draft', active: workflow!.active),
        if (width > 940) const SizedBox(width: 20),
        IconButton(onPressed: undo.isEmpty ? null : undoEdit, tooltip: 'Undo', icon: const Icon(Icons.undo)), IconButton(onPressed: redo.isEmpty ? null : redoEdit, tooltip: 'Redo', icon: const Icon(Icons.redo)),
        OutlinedButton(onPressed: busy || !dirty ? null : save, child: const Text('Save draft')),
        OutlinedButton(onPressed: busy ? null : publish, child: const Text('Publish')),
        if (workflow!.publishedVersion != null) TextButton.icon(onPressed: busy ? null : () => setActivation(!workflow!.active), icon: Icon(workflow!.active ? Icons.pause_circle_outline : Icons.play_circle_outline), label: Text(workflow!.active ? 'Pause' : 'Activate')),
        FilledButton.icon(onPressed: busy || workflow!.publishedVersion == null ? null : runNow, style: FilledButton.styleFrom(backgroundColor: pine), icon: const Icon(Icons.play_arrow), label: const Text('Run now')),
      ])),
      Expanded(child: IgnorePointer(ignoring: busy, child: Row(children: [
        if (width > 690) SizedBox(width: 226, child: _palette()),
        Expanded(child: Column(children: [Expanded(child: _canvas()), _runsStrip()])),
        if (width > 970) SizedBox(width: 320, child: _inspector()),
      ]))),
      if (width <= 970 && selected != null) SizedBox(height: 310, child: IgnorePointer(ignoring: busy, child: _inspector())),
    ]);
  }
  Widget _palette() {
    final entries = catalog.isEmpty ? [for (final type in [...triggerTypes, 'set_fields', 'http_request', 'condition', 'send_email']) {'type': type, 'name': nodeLabel(type), 'description': ''}] : catalog;
    return Container(color: Colors.white, child: ListView(padding: const EdgeInsets.all(16), children: [const Text('NODE LIBRARY', style: fieldLabel), const SizedBox(height: 8), const Text('Drag onto canvas', style: TextStyle(fontSize: 12, color: Color(0xff718078))), const SizedBox(height: 12), TextField(onChanged: (value) => setState(() => paletteSearch = value.toLowerCase()), decoration: const InputDecoration(hintText: 'Search nodes', prefixIcon: Icon(Icons.search), isDense: true)), const SizedBox(height: 20),
      for (final category in ['Triggers', 'Actions']) ...[
        Text(category.toUpperCase(), style: fieldLabel), const SizedBox(height: 8),
        ...entries.where((entry) => triggerTypes.contains(entry['type']) == (category == 'Triggers') && '${entry['name'] ?? entry['type']} ${entry['description'] ?? ''}'.toLowerCase().contains(paletteSearch)).map((entry) { final type = '${entry['type']}'; return Draggable<String>(data: type, feedback: Material(color: Colors.transparent, child: SizedBox(width: 210, child: _paletteTile(entry, dragging: true))), childWhenDragging: Opacity(opacity: .45, child: _paletteTile(entry)), child: InkWell(onTap: () => addNode(type, Offset(180.0 + workflow!.draft.nodes.length * 38, 130.0 + workflow!.draft.nodes.length * 42)), child: _paletteTile(entry))); }),
        const SizedBox(height: 20),
      ],
      const Divider(), const SizedBox(height: 12), const Text('TIP', style: fieldLabel), const SizedBox(height: 7), const Text('Click an output, then an input to connect nodes. Click an edge to remove it.', style: TextStyle(fontSize: 12, color: Color(0xff66716b))),
    ]));
  }
  Widget _paletteTile(Json entry, {bool dragging = false}) => Container(margin: const EdgeInsets.only(bottom: 8), padding: const EdgeInsets.all(11), decoration: BoxDecoration(color: dragging ? const Color(0xffe2eee7) : canvas, border: Border.all(color: line), borderRadius: BorderRadius.circular(10)), child: Row(children: [Icon(iconFor('${entry['type']}'), color: triggerTypes.contains(entry['type']) ? coral : pine, size: 19), const SizedBox(width: 9), Expanded(child: Text('${entry['name'] ?? nodeLabel('${entry['type']}')}', style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700))), const Icon(Icons.drag_indicator, size: 15, color: Color(0xff9aa59d))]));
  Future<void> _mobileAddNode() async {
    final entries = catalog.isEmpty ? [for (final type in [...triggerTypes, 'set_fields', 'http_request', 'condition', 'send_email']) {'type': type, 'name': nodeLabel(type)}] : catalog;
    final type = await showModalBottomSheet<String>(context: context, isScrollControlled: true, builder: (context) => SafeArea(child: SizedBox(height: math.min(560, MediaQuery.sizeOf(context).height * .7), child: ListView(children: [const ListTile(title: Text('Add node', style: TextStyle(fontWeight: FontWeight.w800))), ...entries.map((entry) => ListTile(leading: Icon(iconFor('${entry['type']}'), color: pine), title: Text('${entry['name']}'), onTap: () => Navigator.pop(context, '${entry['type']}')))]))));
    if (type != null) addNode(type, Offset(180.0 + workflow!.draft.nodes.length * 38, 130.0 + workflow!.draft.nodes.length * 42));
  }
  Widget _canvas() => LayoutBuilder(builder: (context, constraints) => DragTarget<String>(onAcceptWithDetails: (details) {
    final box = context.findRenderObject() as RenderBox;
    final local = box.globalToLocal(details.offset);
    addNode(details.data, transform.toScene(local));
  }, builder: (context, candidates, _) => Container(color: const Color(0xfff0f0e9), child: Stack(children: [
    InteractiveViewer(transformationController: transform, constrained: false, minScale: .35, maxScale: 2.4, boundaryMargin: const EdgeInsets.all(700), child: SizedBox(width: 2400, height: 1600, child: Stack(children: [
      Positioned.fill(child: GestureDetector(behavior: HitTestBehavior.opaque, onTapDown: (details) => _tapCanvas(details.localPosition), child: CustomPaint(painter: _GraphPainter(workflow!.draft)))),
      for (final node in workflow!.draft.nodes) Positioned(left: node.x, top: node.y, child: _nodeCard(node)),
    ]))),
    Positioned(top: 14, left: 14, child: Container(padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 7), decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(8), border: Border.all(color: line)), child: Text(pendingSource == null ? 'Drag to pan · Scroll to zoom' : 'Now choose an input port', style: const TextStyle(fontSize: 12, color: pine, fontWeight: FontWeight.w700)))),
    if (MediaQuery.sizeOf(context).width <= 690) Positioned(top: 8, right: 10, child: FilledButton.icon(onPressed: _mobileAddNode, style: FilledButton.styleFrom(backgroundColor: pine), icon: const Icon(Icons.add), label: const Text('Add node'))),
    Positioned(bottom: 14, right: 14, child: Container(decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(10), border: Border.all(color: line)), child: Row(mainAxisSize: MainAxisSize.min, children: [IconButton(tooltip: 'Zoom out', onPressed: () => _zoom(.8), icon: const Icon(Icons.remove)), IconButton(tooltip: 'Reset view', onPressed: () => setState(() => transform.value = transform.value.clone()..setIdentity()), icon: const Icon(Icons.center_focus_strong)), IconButton(tooltip: 'Zoom in', onPressed: () => _zoom(1.25), icon: const Icon(Icons.add))]))),
  ]))));
  void _zoom(double factor) { setState(() { final matrix = transform.value.clone(); matrix.scaleByDouble(factor, factor, 1, 1); transform.value = matrix; }); }
  void _tapCanvas(Offset point) {
    for (final edge in List<WorkflowEdge>.from(workflow!.draft.edges).reversed) {
      final a = workflow!.draft.nodes.where((n) => n.id == edge.source).firstOrNull;
      final b = workflow!.draft.nodes.where((n) => n.id == edge.target).firstOrNull;
      if (a == null || b == null) continue;
      final start = Offset(a.x + 216, a.y + (edge.sourcePort == 'false' ? 93 : edge.sourcePort == 'true' ? 70 : 57));
      final end = Offset(b.x, b.y + 57);
      for (int i = 0; i <= 24; i++) {
        final p = _bezier(start, end, i / 24);
        if ((p - point).distance < 11) { edit((draft) => draft.edges.removeWhere((e) => e.id == edge.id)); showSuccess(context, 'Connection removed'); return; }
      }
    }
    setState(() { selectedId = null; pendingSource = null; pendingPort = null; });
  }
  Widget _nodeCard(WorkflowNode node) {
    final chosen = selectedId == node.id;
    final condition = node.type == 'condition';
    return GestureDetector(onPanStart: (_) => snapshot(), onPanUpdate: (details) { final scale = transform.value.getMaxScaleOnAxis(); setState(() { node.x = (node.x + details.delta.dx / scale).clamp(0, 2180); node.y = (node.y + details.delta.dy / scale).clamp(0, 1450); dirty = true; }); }, child: Container(width: 216, height: condition ? 122 : 102, decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(13), border: Border.all(color: chosen ? pine : line, width: chosen ? 2 : 1), boxShadow: const [BoxShadow(color: Color(0x15000000), blurRadius: 14, offset: Offset(0, 5))]), child: Stack(clipBehavior: Clip.none, children: [
      InkWell(onTap: () => setState(() => selectedId = node.id), child: Padding(padding: const EdgeInsets.fromLTRB(15, 15, 15, 10), child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [Row(children: [Container(width: 32, height: 32, decoration: BoxDecoration(color: triggerTypes.contains(node.type) ? const Color(0xffffe9dc) : const Color(0xffe3ede8), borderRadius: BorderRadius.circular(8)), child: Icon(iconFor(node.type), size: 18, color: triggerTypes.contains(node.type) ? const Color(0xffbd5b3e) : pine)), const SizedBox(width: 9), Expanded(child: Text(nodeLabel(node.type), overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800))), const Icon(Icons.drag_indicator, color: Color(0xffacb4ad), size: 16)]), const SizedBox(height: 10), Text(node.type == 'condition' ? 'Branch by condition' : triggerTypes.contains(node.type) ? 'Starts your workflow' : 'Configure in the right panel', style: const TextStyle(color: Color(0xff77847c), fontSize: 10))]))),
      if (!triggerTypes.contains(node.type)) Positioned(left: -9, top: 48, child: _port(false, node.id, 'in')),
      if (condition) ...[Positioned(right: -9, top: 61, child: _port(true, node.id, 'true')), Positioned(right: -9, top: 84, child: _port(true, node.id, 'false')), const Positioned(right: 17, top: 66, child: Text('T', style: TextStyle(fontSize: 10, color: pine))), const Positioned(right: 17, top: 89, child: Text('F', style: TextStyle(fontSize: 10, color: pine)))]
      else Positioned(right: -9, top: 48, child: _port(true, node.id, 'out')),
    ])));
  }
  Widget _port(bool output, String nodeId, String port) => Tooltip(message: output ? 'Connect $port output' : 'Connect input', child: GestureDetector(onTap: () { if (output) { setState(() { pendingSource = nodeId; pendingPort = port; selectedId = nodeId; }); } else { connect(nodeId); } }, child: Container(width: 18, height: 18, decoration: BoxDecoration(color: pendingSource == nodeId && pendingPort == port ? coral : pine, shape: BoxShape.circle, border: Border.all(color: Colors.white, width: 3)), child: const SizedBox.expand())));
  Widget _runsStrip() => Container(
    height: 64, color: Colors.white, padding: const EdgeInsets.symmetric(horizontal: 16),
    child: Row(children: [
      const Icon(Icons.history, size: 19, color: pine), const SizedBox(width: 7),
      const Text('Runs', style: TextStyle(fontWeight: FontWeight.w800)), const SizedBox(width: 14),
      Expanded(child: runs.isEmpty
        ? const Text('No runs yet', style: TextStyle(color: Color(0xff7a857e), fontSize: 12))
        : ListView(scrollDirection: Axis.horizontal, children: runs.take(8).map((run) => Padding(
            padding: const EdgeInsets.only(right: 8, top: 12, bottom: 12),
            child: ActionChip(
              label: Text('${run['status'] ?? 'queued'} · ${'${run['id']}'.substring(0, math.min(8, '${run['id']}'.length))}'),
              onPressed: () => widget.onNavigate('/runs/${run['id']}'),
            ),
          )).toList())),
      IconButton(tooltip: 'Refresh runs', onPressed: () async { final items = await widget.api.runs(workflowId: workflow!.id); if (mounted) setState(() => runs = items); }, icon: const Icon(Icons.refresh, size: 19)),
    ]),
  );
  Widget _inspector() {
    final node = selected;
    return Container(color: Colors.white, child: node == null ? const Center(child: Padding(padding: EdgeInsets.all(24), child: Column(mainAxisSize: MainAxisSize.min, children: [Icon(Icons.tune, color: pine, size: 30), SizedBox(height: 10), Text('Select a node', style: TextStyle(fontWeight: FontWeight.w800)), SizedBox(height: 5), Text('Edit its settings here.', style: TextStyle(color: Color(0xff748078)))]))) : ListView(key: ValueKey('${node.id}-$configRevision'), padding: const EdgeInsets.all(18), children: [
      Row(children: [Icon(iconFor(node.type), color: pine), const SizedBox(width: 9), Expanded(child: Text(nodeLabel(node.type), style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w800))), IconButton(tooltip: 'Delete node', onPressed: () => removeNode(node), icon: const Icon(Icons.delete_outline, color: Color(0xffa65045)))]),
      Text('ID: ${node.id}', style: const TextStyle(fontSize: 10, color: Color(0xff77847c))), const SizedBox(height: 18),
      if (credentialTypes.contains(node.type)) ...[
        const Text('GMAIL CREDENTIAL', style: fieldLabel), const SizedBox(height: 6),
        DropdownButtonFormField<String>(initialValue: credentials.any((c) => c['id'] == node.credentialId) ? node.credentialId : null, hint: const Text('Choose credential'), items: credentials.map((c) => DropdownMenuItem(value: '${c['id']}', child: Text('${c['name']}'))).toList(), onChanged: (id) => edit((_) => node.credentialId = id)),
        const SizedBox(height: 7), TextButton(onPressed: () => widget.onNavigate('/credentials'), child: const Text('Manage credentials →')), const SizedBox(height: 12),
      ],
      ..._fields(node), const SizedBox(height: 12), OutlinedButton.icon(onPressed: () => _jsonDialog(node), icon: const Icon(Icons.data_object), label: const Text('Edit raw JSON')),
      const SizedBox(height: 10), if (node.type == 'webhook_trigger' && workflow!.publishedVersion != null) SelectableText('Webhook: /api/hooks/${workflow!.id}', style: const TextStyle(fontSize: 12, color: pine)),
      const SizedBox(height: 8), const Text('Mappings: {{input.field}} or {{nodes.NODE_ID.field}}', style: TextStyle(color: Color(0xff7c867f), fontSize: 11)),
      TextButton.icon(onPressed: () => _insertMapping(node), icon: const Icon(Icons.auto_fix_high, size: 17), label: const Text('Browse available mappings')),
    ]));
  }
  List<Widget> _fields(WorkflowNode node) {
    final result = <Widget>[];
    void field(String key, String label, {int maxLines = 1, String? hint, bool number = false}) {
      result.add(Padding(padding: const EdgeInsets.only(bottom: 15), child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [Text(label.toUpperCase(), style: fieldLabel), const SizedBox(height: 7), TextFormField(key: ValueKey('${node.id}-$key'), initialValue: '${node.config[key] ?? ''}', maxLines: maxLines, keyboardType: number ? TextInputType.number : TextInputType.text, decoration: InputDecoration(hintText: hint), onChanged: (value) => edit((_) => node.config[key] = number ? (int.tryParse(value) ?? 0) : value))])));
    }
    switch (node.type) {
      case 'webhook_trigger': field('secret', 'Webhook secret', hint: 'Shared secret for X-Webhook-Secret');
      case 'schedule_trigger': field('interval_seconds', 'Interval seconds', number: true); field('timezone', 'Timezone', hint: 'UTC');
      case 'email_trigger': field('poll_seconds', 'Poll seconds', number: true); field('query', 'Gmail query', hint: 'is:unread');
      case 'set_fields':
        result.add(_objectField(node, 'fields', 'FIELDS', 'Add field', 'name', 'value'));
      case 'http_request':
        field('url', 'URL', hint: 'https://api.example.com');
        result.add(Padding(padding: const EdgeInsets.only(bottom: 14), child: DropdownButtonFormField<String>(initialValue: ['GET','POST','PUT','PATCH','DELETE'].contains(node.config['method']) ? '${node.config['method']}' : 'GET', decoration: const InputDecoration(labelText: 'Method'), items: ['GET','POST','PUT','PATCH','DELETE'].map((v) => DropdownMenuItem(value: v, child: Text(v))).toList(), onChanged: (v) => edit((_) => node.config['method'] = v))));
        result.add(_objectField(node, 'headers', 'HEADERS', 'Add header', 'Header', 'Value')); field('body', 'Body', maxLines: 5, hint: 'Optional request body');
      case 'condition':
        field('left', 'Left value', hint: '{{input.status}}');
        result.add(Padding(padding: const EdgeInsets.only(bottom: 14), child: DropdownButtonFormField<String>(initialValue: ['equals','contains','greater_than','exists'].contains(node.config['operator']) ? '${node.config['operator']}' : 'equals', decoration: const InputDecoration(labelText: 'Operator'), items: ['equals','contains','greater_than','exists'].map((v) => DropdownMenuItem(value: v, child: Text(v.replaceAll('_', ' ')))).toList(), onChanged: (v) => edit((_) => node.config['operator'] = v))));
        field('right', 'Right value', hint: 'expected');
      case 'send_email': field('to', 'To', hint: 'person@example.com'); field('subject', 'Subject'); field('body', 'Body', maxLines: 6);
      default: result.add(const Text('This trigger needs no configuration.', style: TextStyle(color: Color(0xff76817b))));
    }
    return result;
  }
  Widget _objectField(WorkflowNode node, String key, String title, String action, String keyHint, String valueHint) {
    final map = asJson(node.config[key]);
    return Padding(padding: const EdgeInsets.only(bottom: 15), child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [Text(title, style: fieldLabel), const SizedBox(height: 6), ...map.entries.map((entry) => Padding(padding: const EdgeInsets.only(bottom: 5), child: Row(children: [Expanded(child: Text('${entry.key}: ${entry.value}', overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 12))), IconButton(tooltip: 'Remove ${entry.key}', onPressed: () => edit((_) { map.remove(entry.key); node.config[key] = map; }), icon: const Icon(Icons.close, size: 16))]))), TextButton.icon(onPressed: () async {
      final name = TextEditingController(), value = TextEditingController();
      final pair = await showDialog<List<String>>(context: context, builder: (context) => AlertDialog(title: Text(action), content: Column(mainAxisSize: MainAxisSize.min, children: [TextField(controller: name, decoration: InputDecoration(labelText: keyHint)), const SizedBox(height: 10), TextField(controller: value, decoration: InputDecoration(labelText: valueHint))]), actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')), FilledButton(onPressed: () => Navigator.pop(context, [name.text, value.text]), child: const Text('Add'))]));
      disposeDialogController(name); disposeDialogController(value); if (pair != null && pair[0].trim().isNotEmpty) edit((_) { map[pair[0].trim()] = pair[1]; node.config[key] = map; });
    }, icon: const Icon(Icons.add, size: 18), label: Text(action))]));
  }
  Future<void> _jsonDialog(WorkflowNode node) async {
    final controller = TextEditingController(text: const JsonEncoder.withIndent('  ').convert(node.config)); String? localError;
    final config = await showDialog<Json>(context: context, builder: (context) => StatefulBuilder(builder: (context, setDialog) => AlertDialog(title: Text('Configure ${nodeLabel(node.type)}'), content: SizedBox(width: 520, child: Column(mainAxisSize: MainAxisSize.min, children: [TextField(controller: controller, maxLines: 14, style: const TextStyle(fontFamily: 'monospace', fontSize: 12), decoration: const InputDecoration(border: OutlineInputBorder())), if (localError != null) Text(localError!, style: const TextStyle(color: Colors.red))])), actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')), FilledButton(onPressed: () { try { final value = jsonDecode(controller.text); if (value is! Map) throw const FormatException(); Navigator.pop(context, Map<String, dynamic>.from(value)); } catch (_) { setDialog(() => localError = 'Enter a valid JSON object.'); } }, child: const Text('Apply'))])));
    disposeDialogController(controller); if (config != null) { edit((_) => node.config = config); setState(() => configRevision++); }
  }
  Future<void> _insertMapping(WorkflowNode node) async {
    final ancestors = <String>{};
    void trace(String id) {
      for (final edge in workflow!.draft.edges.where((e) => e.target == id)) {
        if (ancestors.add(edge.source)) trace(edge.source);
      }
    }
    trace(node.id);
    Json lastRun = {}, outputs = {};
    if (runs.isNotEmpty) {
      try {
        final detail = await widget.api.runDetail('${runs.first['id']}');
        lastRun = asJson(detail['run']);
        for (final step in asJsonList(detail['steps'])) { outputs['${step['node_id']}'] = asJson(step['output']); }
      } catch (_) { /* Mapping suggestions remain available from the graph. */ }
    }
    if (!mounted) return;
    final values = <String>[];
    for (final key in asJson(lastRun['input']).keys) { values.add('{{input.$key}}'); }
    for (final upstream in workflow!.draft.nodes.where((n) => ancestors.contains(n.id))) {
      final fields = <String>{...asJson(outputs[upstream.id]).keys};
      if (upstream.type == 'set_fields') fields.addAll(asJson(upstream.config['fields']).keys);
      for (final key in fields) { values.add('{{nodes.${upstream.id}.$key}}'); }
    }
    final custom = TextEditingController();
    final mapping = await showDialog<String>(context: context, builder: (context) => AlertDialog(title: const Text('Insert a mapping'), content: SizedBox(width: 510, child: ListView(shrinkWrap: true, children: [
      const Text('Suggestions use upstream nodes and the latest run. Enter a field name when no sample is available.', style: TextStyle(fontSize: 12, color: Color(0xff66716b))), const SizedBox(height: 10),
      ...values.map((value) => ListTile(dense: true, title: Text(value, style: const TextStyle(fontFamily: 'monospace', fontSize: 12)), onTap: () => Navigator.pop(context, value))),
      const Divider(), TextField(controller: custom, decoration: const InputDecoration(labelText: 'Run input field name', hintText: 'customer_id')),
    ])), actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')), FilledButton(onPressed: () { if (custom.text.trim().isNotEmpty) Navigator.pop(context, '{{input.${custom.text.trim()}}}'); }, child: const Text('Use input field'))]));
    disposeDialogController(custom); if (mapping == null || !mounted) return;
    final targets = <String>[for (final entry in node.config.entries) if (entry.value is String) entry.key];
    for (final group in ['fields', 'headers']) { for (final key in asJson(node.config[group]).keys) { targets.add('$group.$key'); } }
    if (targets.isEmpty) { showError(context, 'Add a text field to this node first.'); return; }
    final target = await showDialog<String>(context: context, builder: (context) => SimpleDialog(title: const Text('Insert into which setting?'), children: targets.map((field) => SimpleDialogOption(onPressed: () => Navigator.pop(context, field), child: Text(field))).toList()));
    if (target == null) return;
    edit((_) {
      if (target.contains('.')) { final parts = target.split('.'); final map = asJson(node.config[parts[0]]); map[parts[1]] = '${map[parts[1]] ?? ''}$mapping'; node.config[parts[0]] = map; }
      else { node.config[target] = '${node.config[target] ?? ''}$mapping'; }
    });
    setState(() => configRevision++);
  }
}

IconData iconFor(String type) => switch (type) {
  'manual_trigger' => Icons.touch_app_outlined,
  'webhook_trigger' => Icons.webhook,
  'schedule_trigger' => Icons.schedule,
  'email_trigger' => Icons.mark_email_unread_outlined,
  'set_fields' => Icons.table_rows_outlined,
  'http_request' => Icons.language,
  'condition' => Icons.alt_route,
  'send_email' => Icons.send_outlined,
  _ => Icons.extension_outlined,
};

Offset _bezier(Offset a, Offset b, double t) {
  final curve = math.max(80.0, (b.dx - a.dx).abs() * .5);
  final c1 = Offset(a.dx + curve, a.dy), c2 = Offset(b.dx - curve, b.dy);
  final u = 1 - t;
  return a * (u * u * u) + c1 * (3 * u * u * t) + c2 * (3 * u * t * t) + b * (t * t * t);
}
class _GraphPainter extends CustomPainter {
  _GraphPainter(this.draft); final WorkflowDraft draft;
  @override void paint(Canvas canvas, Size size) {
    final grid = Paint()..color = const Color(0xffd5dbd2)..strokeWidth = 1;
    for (double x = 0; x < size.width; x += 24) { for (double y = 0; y < size.height; y += 24) { canvas.drawCircle(Offset(x, y), 1, grid); } }
    for (final edge in draft.edges) {
      final source = draft.nodes.where((n) => n.id == edge.source).firstOrNull;
      final target = draft.nodes.where((n) => n.id == edge.target).firstOrNull;
      if (source == null || target == null) continue;
      final a = Offset(source.x + 216, source.y + (edge.sourcePort == 'false' ? 93 : edge.sourcePort == 'true' ? 70 : 57));
      final b = Offset(target.x, target.y + 57);
      final curve = math.max(80.0, (b.dx - a.dx).abs() * .5);
      final path = Path()..moveTo(a.dx, a.dy)..cubicTo(a.dx + curve, a.dy, b.dx - curve, b.dy, b.dx, b.dy);
      canvas.drawPath(path, Paint()..color = pine..strokeWidth = 3..style = PaintingStyle.stroke..strokeCap = StrokeCap.round);
      canvas.drawCircle(b, 5, Paint()..color = pine);
    }
  }
  @override bool shouldRepaint(covariant _GraphPainter oldDelegate) => true;
}
