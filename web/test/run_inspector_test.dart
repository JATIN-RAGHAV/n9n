import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:n9n_web/api.dart';
import 'package:n9n_web/main.dart';

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
    expect(find.text('3 ms'), findsOneWidget);
    expect(find.text('1.5 s'), findsOneWidget);
    expect(find.text('succeeded · attempt 1 · 3 requests'), findsOneWidget);
    expect(find.text('ATTEMPTS (3)'), findsNothing);

    await tester.tap(find.textContaining('HTTP request · call'));
    await tester.pumpAndSettle();
    expect(find.text('ATTEMPTS (3)'), findsOneWidget);
    expect(find.textContaining('HTTP 500'), findsNothing);

    await tester.tap(find.text('ATTEMPTS (3)'));
    await tester.pumpAndSettle();
    expect(find.text('Attempt 1 · failed · HTTP 500 · 210 ms'), findsOneWidget);
    expect(find.text('HTTP 500 Internal Server Error: {}'), findsOneWidget);
    expect(find.text('Attempt 3 · succeeded · HTTP 200 · 80 ms'),
        findsOneWidget);
  });

  test('durations read naturally at every scale', () {
    expect(formatDurationMs(0), '0 ms');
    expect(formatDurationMs(999), '999 ms');
    expect(formatDurationMs(1480), '1.5 s');
    expect(formatDurationMs(12400), '12 s');
    expect(formatDurationMs(90000), '1.5 min');
  });
}
