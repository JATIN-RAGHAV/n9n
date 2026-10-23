import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:n9n_web/api.dart';
import 'package:n9n_web/main.dart';
import 'package:n9n_web/models.dart';

/// A finished run whose HTTP node needed three requests.
const runDetail = {
  'run': {
    'id': 'run-1',
    'workflow_id': 'flow-1',
    'version': 1,
    'kind': 'normal',
    'status': 'succeeded',
    'input': {},
    'created_at': '2026-01-02T03:04:05Z',
    'updated_at': '2026-01-02T03:04:07Z'
  },
  'steps': [
    {
      'node_id': 'start',
      'status': 'succeeded',
      'input': {},
      'output': {},
      'attempt': 1,
      'duration_ms': 3,
      'created_at': '2026-01-02T03:04:05Z'
    },
    {
      'node_id': 'call',
      'status': 'succeeded',
      'input': {},
      'output': {'status': 200},
      'attempt': 1,
      'duration_ms': 1480,
      'attempts': [
        {
          'attempt': 1,
          'status': 'failed',
          'http_status': 500,
          'error': 'HTTP 500 Internal Server Error: {}',
          'duration_ms': 210,
          'created_at': '2026-01-02T03:04:05Z'
        },
        {
          'attempt': 2,
          'status': 'failed',
          'http_status': 503,
          'error': 'HTTP 503 Service Unavailable: {}',
          'duration_ms': 190,
          'created_at': '2026-01-02T03:04:06Z'
        },
        {
          'attempt': 3,
          'status': 'succeeded',
          'http_status': 200,
          'duration_ms': 80,
          'created_at': '2026-01-02T03:04:07Z'
        }
      ],
      'created_at': '2026-01-02T03:04:07Z'
    }
  ],
  'node_types': {'start': 'manual_trigger', 'call': 'http_request'}
};

MockClient inspectorClient() => MockClient((request) async {
      final payload = switch (request.url.path) {
        '/api/auth/me' => {
            'user': {'id': 'user-1', 'email': 'me@example.com'}
          },
        '/api/runs/run-1' => runDetail,
        _ => {'runs': [], 'workflows': []},
      };
      return http.Response(jsonEncode(payload), 200,
          headers: {'content-type': 'application/json'});
    });

/// A failed run: trigger -> a (set_fields) -> b (http_request), failing at b.
final failedRunDetail = {
  'run': {
    'id': 'run-2',
    'workflow_id': 'flow-1',
    'version': 1,
    'kind': 'normal',
    'status': 'failed',
    'input': {'seed': 1},
    'error': 'boom',
    'created_at': '2026-01-02T03:04:05Z',
    'updated_at': '2026-01-02T03:04:07Z'
  },
  'steps': [
    {
      'node_id': 'trigger',
      'status': 'succeeded',
      'input': {'seed': 1},
      'output': {'seed': 1},
      'attempt': 1,
      'duration_ms': 1,
      'created_at': '2026-01-02T03:04:05Z'
    },
    {
      'node_id': 'a',
      'status': 'running',
      'input': {'seed': 1},
      'output': null,
      'attempt': 1,
      'created_at': '2026-01-02T03:04:05Z'
    },
    {
      'node_id': 'a',
      'status': 'succeeded',
      'input': {'seed': 1},
      'output': {'stage': 'alpha-output'},
      'attempt': 1,
      'duration_ms': 20,
      'created_at': '2026-01-02T03:04:06Z'
    },
    {
      'node_id': 'b',
      'status': 'failed',
      'input': {'stage': 'alpha-output'},
      'output': null,
      'error': 'boom',
      'attempt': 1,
      'duration_ms': 5,
      'created_at': '2026-01-02T03:04:07Z'
    }
  ],
  'node_types': {
    'trigger': 'manual_trigger',
    'a': 'set_fields',
    'b': 'http_request'
  },
  'graph': {
    'nodes': [
      {
        'id': 'trigger',
        'type': 'manual_trigger',
        'position': {'x': 100, 'y': 100},
        'config': {}
      },
      {
        'id': 'a',
        'type': 'set_fields',
        'position': {'x': 400, 'y': 100},
        'config': {'fields': {}}
      },
      {
        'id': 'b',
        'type': 'http_request',
        'position': {'x': 700, 'y': 100},
        'config': {'url': 'https://example.com', 'method': 'GET'}
      },
    ],
    'edges': [
      {'id': 'e1', 'source': 'trigger', 'target': 'a', 'source_port': 'out'},
      {'id': 'e2', 'source': 'a', 'target': 'b', 'source_port': 'out'},
    ]
  },
  'children': [],
};

void main() {
  testWidgets('run inspector shows step durations and retry attempts',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(N9nApp(
      api: Api(client: inspectorClient()),
      initialUri: Uri.parse('http://localhost/#/runs/run-1'),
    ));
    await tester.pumpAndSettle();
    expect(find.text('STEP BY STEP'), findsOneWidget);
    // Durations show on the timeline, the moment panel and the step card.
    expect(find.text('3 ms'), findsWidgets);
    expect(find.text('1.5 s'), findsWidgets);
    expect(find.text('succeeded · attempt 1 · 3 requests'), findsOneWidget);
    expect(find.text('ATTEMPTS (3)'), findsNothing);

    final stepCard = find.descendant(
        of: find.byType(ExpansionTile),
        matching: find.text('HTTP request · call'));
    await tester.ensureVisible(stepCard);
    await tester.pumpAndSettle();
    await tester.tap(stepCard);
    await tester.pumpAndSettle();
    expect(find.text('ATTEMPTS (3)'), findsOneWidget);
    expect(find.textContaining('HTTP 500'), findsNothing);

    await tester.ensureVisible(find.text('ATTEMPTS (3)'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('ATTEMPTS (3)'));
    await tester.pumpAndSettle();
    expect(find.text('Attempt 1 · failed · HTTP 500 · 210 ms'), findsOneWidget);
    expect(find.text('HTTP 500 Internal Server Error: {}'), findsOneWidget);
    expect(find.text('Attempt 3 · succeeded · HTTP 200 · 80 ms'),
        findsOneWidget);
  });

  testWidgets('timeline scrubber selects steps and highlights canvas nodes',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final client = MockClient((request) async {
      final payload = switch (request.url.path) {
        '/api/auth/me' => {
            'user': {'id': 'user-1', 'email': 'me@example.com'}
          },
        '/api/runs/run-2' => failedRunDetail,
        _ => {'runs': [], 'workflows': []},
      };
      return http.Response(jsonEncode(payload), 200,
          headers: {'content-type': 'application/json'});
    });
    await tester.pumpWidget(N9nApp(
      api: Api(client: client),
      initialUri: Uri.parse('http://localhost/#/runs/run-2'),
    ));
    await tester.pumpAndSettle();

    // One timeline entry per executed step; the failure is selected first.
    expect(find.byKey(const ValueKey('timeline:0')), findsOneWidget);
    expect(find.byKey(const ValueKey('timeline:2')), findsOneWidget);
    expect(find.byKey(const ValueKey('timeline:3')), findsNothing);
    final moment = find.byKey(const ValueKey('moment'));
    expect(moment, findsOneWidget);
    expect(find.descendant(of: moment, matching: find.text('AT THIS MOMENT')),
        findsOneWidget);
    expect(
        find.descendant(
            of: moment, matching: find.text('3. HTTP request · b')),
        findsOneWidget);
    expect(find.descendant(of: moment, matching: find.text('boom')),
        findsOneWidget);
    BoxDecoration canvasDecoration(String nodeId) =>
        tester.widget<Container>(find.byKey(ValueKey('canvas:$nodeId')))
            .decoration as BoxDecoration;
    expect(canvasDecoration('b').border!.top.width, 3);
    expect(canvasDecoration('a').border!.top.width, 1.5);
    expect(find.text('not reached'), findsNothing);

    // Scrubbing to an earlier step shows what it produced then.
    await tester.tap(find.byKey(const ValueKey('timeline:1')));
    await tester.pumpAndSettle();
    expect(
        find.descendant(of: moment, matching: find.text('2. Set fields · a')),
        findsOneWidget);
    expect(
        find.descendant(
            of: moment, matching: find.textContaining('alpha-output')),
        findsOneWidget);
    expect(canvasDecoration('a').border!.top.width, 3);
    expect(canvasDecoration('b').border!.top.width, 1.5);

    // Tapping a canvas node selects its step.
    await tester.tap(find.byKey(const ValueKey('canvas:trigger')));
    await tester.pumpAndSettle();
    expect(
        find.descendant(
            of: moment, matching: find.text('1. Manual trigger · trigger')),
        findsOneWidget);
    expect(canvasDecoration('trigger').border!.top.width, 3);
  });

  /// Serves the failed run, records replay requests and serves the new run.
  MockClient replayClient(Map<String, Object?> calls,
          {Object? draft, int draftStatus = 200}) =>
      MockClient((request) async {
        final path = request.url.path;
        var status = 200;
        final replayed = {
          'id': 'run-3',
          'workflow_id': 'flow-1',
          'version': 1,
          'kind': 'replay',
          'parent_run_id': 'run-2',
          'resume_from_node': 'b',
          'status': 'succeeded',
          'input': {'seed': 1},
          'created_at': '2026-01-02T03:05:05Z',
          'updated_at': '2026-01-02T03:05:07Z'
        };
        Object payload;
        if (path == '/api/auth/me') {
          payload = {
            'user': {'id': 'user-1', 'email': 'me@example.com'}
          };
        } else if (path == '/api/runs/run-2/replay' &&
            request.method == 'POST') {
          calls['replay'] = jsonDecode(request.body);
          payload = {'run': replayed};
        } else if (path == '/api/runs/run-2') {
          payload = failedRunDetail;
        } else if (path == '/api/workflows/flow-1' && draft != null) {
          calls['draft'] = (calls['draft'] as int? ?? 0) + 1;
          status = draftStatus;
          payload = status == 200
              ? {
                  'workflow': {
                    'id': 'flow-1',
                    'name': 'Flow',
                    'draft': draft,
                    'active': false
                  }
                }
              : {'error': 'database error'};
        } else if (path == '/api/runs/run-3') {
          payload = {
            'run': replayed,
            'steps': (failedRunDetail['steps'] as List).take(1).toList(),
            'node_types': failedRunDetail['node_types'],
            'graph': failedRunDetail['graph'],
            'children': [],
          };
        } else {
          payload = {'runs': [], 'workflows': []};
        }
        return http.Response(jsonEncode(payload), status,
            headers: {'content-type': 'application/json'});
      });

  // The current draft turns b into a plain transform and adds an HTTP node c.
  final editedDraft = {
    'nodes': [
      {
        'id': 'trigger',
        'type': 'manual_trigger',
        'position': {'x': 100, 'y': 100},
        'config': {}
      },
      {
        'id': 'a',
        'type': 'set_fields',
        'position': {'x': 400, 'y': 100},
        'config': {'fields': {}}
      },
      {
        'id': 'b',
        'type': 'set_fields',
        'position': {'x': 700, 'y': 100},
        'config': {'fields': {}}
      },
      {
        'id': 'c',
        'type': 'http_request',
        'position': {'x': 1000, 'y': 100},
        'config': {'url': 'https://example.com', 'method': 'POST'}
      },
    ],
    'edges': [
      {'id': 'e1', 'source': 'trigger', 'target': 'a', 'source_port': 'out'},
      {'id': 'e2', 'source': 'a', 'target': 'b', 'source_port': 'out'},
      {'id': 'e3', 'source': 'b', 'target': 'c', 'source_port': 'out'},
    ]
  };

  testWidgets('the side-effect warning follows the graph that will run',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String, Object?>{};
    await tester.pumpWidget(N9nApp(
      api: Api(client: replayClient(calls, draft: editedDraft)),
      initialUri: Uri.parse('http://localhost/#/runs/run-2'),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Replay against current draft'));
    await tester.pumpAndSettle();
    // Against the draft, b no longer calls out but the new node c does.
    expect(find.textContaining('This replay executes HTTP request · c'),
        findsOneWidget);
    expect(find.textContaining('HTTP request · b.'), findsNothing);
    await tester.tap(find.text('Original graph'));
    await tester.pumpAndSettle();
    expect(find.textContaining('This replay executes HTTP request · b'),
        findsOneWidget);
    await tester.tap(find.text('Current draft'));
    await tester.pumpAndSettle();
    expect(find.textContaining('This replay executes HTTP request · c'),
        findsOneWidget);
    expect(calls['draft'], 1, reason: 'the draft is fetched once');
  });

  testWidgets('an unavailable draft still warns before a replay',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String, Object?>{};
    await tester.pumpWidget(N9nApp(
      api: Api(
          client: replayClient(calls, draft: editedDraft, draftStatus: 500)),
      initialUri: Uri.parse('http://localhost/#/runs/run-2'),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(
        of: find.byKey(const ValueKey('moment')),
        matching: find.text('Re-run from here')));
    await tester.pumpAndSettle();
    expect(find.textContaining('This replay executes HTTP request · b'),
        findsOneWidget);
    await tester.tap(find.text('Current draft'));
    await tester.pumpAndSettle();
    expect(find.textContaining('The current draft could not be loaded'),
        findsOneWidget);
    expect(find.byKey(const ValueKey('replay-warning')), findsOneWidget);
  });

  testWidgets('re-run from here replays the step with the edited input',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String, Object?>{};
    await tester.pumpWidget(N9nApp(
      api: Api(client: replayClient(calls)),
      initialUri: Uri.parse('http://localhost/#/runs/run-2'),
    ));
    await tester.pumpAndSettle();
    // The failed step is selected, so the moment panel replays from b.
    await tester.tap(find.descendant(
        of: find.byKey(const ValueKey('moment')),
        matching: find.text('Re-run from here')));
    await tester.pumpAndSettle();
    expect(find.text('Re-run from HTTP request · b'), findsOneWidget);
    expect(find.byKey(const ValueKey('replay-warning')), findsOneWidget);
    expect(find.textContaining('This replay executes HTTP request · b'),
        findsOneWidget);
    expect(find.textContaining('"stage": "alpha-output"'), findsWidgets);
    await tester.enterText(find.byType(TextField), '{"stage": "fixed"}');
    await tester.tap(find.text('Start replay'));
    await tester.pumpAndSettle();
    final body = calls['replay'] as Map;
    expect(body['from_node'], 'b');
    expect(body['graph'], 'original');
    expect(body['input_override'], {'stage': 'fixed'});
    // The new run opens in the inspector with its lineage.
    expect(find.text('Run run-3'), findsOneWidget);
    expect(find.text('REPLAY FROM b'), findsOneWidget);
    expect(find.text('Replay of run-2'), findsOneWidget);
  });

  testWidgets('failed runs offer a replay against the current draft',
      (tester) async {
    tester.view.physicalSize = const Size(1400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final calls = <String, Object?>{};
    await tester.pumpWidget(N9nApp(
      api: Api(client: replayClient(calls)),
      initialUri: Uri.parse('http://localhost/#/runs/run-2'),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Replay against current draft'));
    await tester.pumpAndSettle();
    expect(find.text('Re-run from HTTP request · b'), findsOneWidget);
    final draftChip = tester.widget<ChoiceChip>(
        find.widgetWithText(ChoiceChip, 'Current draft'));
    expect(draftChip.selected, isTrue);
    // Invalid JSON is caught before anything is sent.
    await tester.enterText(find.byType(TextField), '{not json');
    await tester.tap(find.text('Start replay'));
    await tester.pumpAndSettle();
    expect(find.text('Enter a valid JSON object.'), findsOneWidget);
    expect(calls, isEmpty);
    // Unchanged input sends no override.
    await tester.enterText(find.byType(TextField), '{"stage": "alpha-output"}');
    await tester.tap(find.text('Start replay'));
    await tester.pumpAndSettle();
    final body = calls['replay'] as Map;
    expect(body['graph'], 'draft');
    expect(body['from_node'], 'b');
    expect(body.containsKey('input_override'), isFalse);
    expect(find.text('Run run-3'), findsOneWidget);
  });

  test('downstream nodes list the step and everything after it', () {
    final draft = WorkflowDraft.fromJson(
        Map<String, dynamic>.from(failedRunDetail['graph'] as Map));
    expect(downstreamNodes(draft, 'a').map((n) => n.id), ['a', 'b']);
    expect(downstreamNodes(draft, 'b').map((n) => n.id), ['b']);
    expect(downstreamNodes(draft, 'ghost'), isEmpty);
  });

  test('durations read naturally at every scale', () {
    expect(formatDurationMs(0), '0 ms');
    expect(formatDurationMs(999), '999 ms');
    expect(formatDurationMs(1480), '1.5 s');
    expect(formatDurationMs(12400), '12 s');
    expect(formatDurationMs(90000), '1.5 min');
  });
}
