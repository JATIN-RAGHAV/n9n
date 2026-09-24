import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:n9n_web/api.dart';
import 'package:n9n_web/main.dart';

void main() {
  testWidgets('outer halves of normal and condition ports connect',
      (tester) async {
    tester.view.physicalSize = const Size(1600, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final draft = <String, dynamic>{
      'nodes': [
        {
          'id': 'trigger',
          'type': 'manual_trigger',
          'position': {'x': 100, 'y': 100},
          'config': {}
        },
        {
          'id': 'branch',
          'type': 'condition',
          'position': {'x': 420, 'y': 100},
          'config': {'left': '1', 'operator': 'equals', 'right': '1'}
        },
        {
          'id': 'yes',
          'type': 'set_fields',
          'position': {'x': 760, 'y': 100},
          'config': {'fields': {}}
        },
        {
          'id': 'no',
          'type': 'set_fields',
          'position': {'x': 760, 'y': 300},
          'config': {'fields': {}}
        },
      ],
      'edges': <dynamic>[],
    };
    Map<String, dynamic>? savedDraft;
    final client = MockClient((request) async {
      final path = request.url.path;
      Object payload;
      if (path == '/api/auth/me') {
        payload = {
          'user': {'id': 'user-1', 'email': 'me@example.com'}
        };
      } else if (path == '/api/workflows/flow-ports' &&
          request.method == 'PUT') {
        savedDraft =
            Map<String, dynamic>.from(jsonDecode(request.body)['draft'] as Map);
        payload = {
          'workflow': {
            'id': 'flow-ports',
            'name': 'Port test',
            'draft': savedDraft,
            'active': false
          }
        };
      } else if (path == '/api/workflows/flow-ports') {
        payload = {
          'workflow': {
            'id': 'flow-ports',
            'name': 'Port test',
            'draft': draft,
            'active': false
          }
        };
      } else if (path == '/api/nodes') {
        payload = {'nodes': []};
      } else if (path == '/api/credentials') {
        payload = {'credentials': []};
      } else {
        payload = {'runs': []};
      }
      return http.Response(jsonEncode(payload), 200,
          headers: {'content-type': 'application/json'});
    });
    await tester.pumpWidget(N9nApp(
      api: Api(client: client),
      initialUri: Uri.parse('http://localhost/#/workflows/flow-ports'),
    ));
    await tester.pumpAndSettle();

    Future<void> tapOuter(String id, String port,
        {required bool output}) async {
      final rect = tester.getRect(find.byKey(ValueKey('port:$id:$port')));
      final point =
          Offset(output ? rect.right - 2 : rect.left + 2, rect.center.dy);
      await tester.tapAt(point);
      await tester.pump();
    }

    await tapOuter('trigger', 'out', output: true);
    await tapOuter('branch', 'in', output: false);
    await tapOuter('branch', 'true', output: true);
    await tapOuter('yes', 'in', output: false);
    await tapOuter('branch', 'false', output: true);
    await tapOuter('no', 'in', output: false);
    await tester.tap(find.text('Save draft'));
    await tester.pumpAndSettle();
    final edges = (savedDraft!['edges'] as List).cast<Map>();
    expect(edges, hasLength(3));
    expect(edges.map((edge) => edge['source_port']),
        containsAll(['out', 'true', 'false']));
  });

  testWidgets('deep linked editor survives asynchronous session loading',
      (tester) async {
    final client = MockClient((request) async {
      await Future<void>.delayed(const Duration(milliseconds: 10));
      final path = request.url.path;
      final payload = switch (path) {
        '/api/auth/me' => {
            'user': {'id': 'user-1', 'email': 'me@example.com'}
          },
        '/api/workflows/flow-1' => {
            'workflow': {
              'id': 'flow-1',
              'name': 'Deep link flow',
              'draft': {'nodes': [], 'edges': []},
              'active': false
            }
          },
        '/api/nodes' => {'nodes': []},
        '/api/credentials' => {'credentials': []},
        _ => {'runs': []},
      };
      return http.Response(jsonEncode(payload), 200,
          headers: {'content-type': 'application/json'});
    });
    await tester.pumpWidget(N9nApp(
      api: Api(client: client),
      initialUri: Uri.parse('http://localhost/#/workflows/flow-1'),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Deep link flow'), findsWidgets);
    expect(find.text('NODE LIBRARY'), findsOneWidget);
  });

  testWidgets('register, create a workflow, and open its editor',
      (tester) async {
    final calls = <String>[];
    final client = MockClient((request) async {
      calls.add('${request.method} ${request.url.path}');
      Object payload;
      int status = 200;
      switch ('${request.method} ${request.url.path}') {
        case 'GET /api/auth/me':
          status = 401;
          payload = {'error': 'authentication required'};
        case 'POST /api/auth/register':
          payload = {
            'user': {'id': 'user-1', 'email': 'me@example.com'}
          };
        case 'GET /api/workflows':
          payload = {'workflows': []};
        case 'GET /api/runs':
          payload = {'runs': []};
        case 'POST /api/workflows':
          payload = {
            'workflow': {
              'id': 'flow-1',
              'name': 'Welcome flow',
              'draft': {'nodes': [], 'edges': []},
              'active': false
            }
          };
        case 'GET /api/workflows/flow-1':
          payload = {
            'workflow': {
              'id': 'flow-1',
              'name': 'Welcome flow',
              'draft': {'nodes': [], 'edges': []},
              'active': false
            }
          };
        case 'GET /api/nodes':
          payload = {'nodes': []};
        case 'GET /api/credentials':
          payload = {'credentials': []};
        default:
          payload = {'runs': []};
      }
      return http.Response(jsonEncode(payload), status,
          headers: {'content-type': 'application/json'});
    });
    await tester.pumpWidget(N9nApp(api: Api(client: client)));
    await tester.pumpAndSettle();
    expect(find.text('Welcome back'), findsOneWidget);
    await tester.tap(find.text('New here? Create an account'));
    await tester.enterText(find.byType(TextField).at(0), 'me@example.com');
    await tester.enterText(find.byType(TextField).at(1), 'password123');
    await tester.tap(find.text('Create account'));
    await tester.pumpAndSettle();
    expect(find.text('Workflows'), findsWidgets);
    await tester.tap(find.text('New workflow'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).last, 'Welcome flow');
    await tester.tap(find.text('Create'));
    await tester.pumpAndSettle();
    expect(find.text('Welcome flow'), findsWidgets);
    expect(find.text('NODE LIBRARY'), findsOneWidget);
    expect(calls, contains('POST /api/auth/register'));
    expect(calls, contains('POST /api/workflows'));
    expect(calls, contains('GET /api/workflows/flow-1'));
  });
}
