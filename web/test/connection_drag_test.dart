import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:n9n_web/api.dart';
import 'package:n9n_web/main.dart';

void main() {
  testWidgets('dragging an output to an input connects without moving nodes',
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
          'id': 'action',
          'type': 'set_fields',
          'position': {'x': 420, 'y': 100},
          'config': {'fields': {}}
        },
        {
          'id': 'next',
          'type': 'set_fields',
          'position': {'x': 760, 'y': 300},
          'config': {'fields': {}}
        }
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
      } else if (path == '/api/workflows/flow-drag' &&
          request.method == 'PUT') {
        savedDraft =
            Map<String, dynamic>.from(jsonDecode(request.body)['draft'] as Map);
        payload = {
          'workflow': {
            'id': 'flow-drag',
            'name': 'Drag test',
            'draft': savedDraft,
            'active': false
          }
        };
      } else if (path == '/api/workflows/flow-drag') {
        payload = {
          'workflow': {
            'id': 'flow-drag',
            'name': 'Drag test',
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
      initialUri: Uri.parse('http://localhost/#/workflows/flow-drag'),
    ));
    await tester.pumpAndSettle();
    final source =
        tester.getCenter(find.byKey(const ValueKey('port:trigger:out')));
    final target =
        tester.getCenter(find.byKey(const ValueKey('port:action:in')));
    final gesture = await tester.startGesture(source);
    await gesture.moveTo(target);
    await tester.pump();
    await gesture.up();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save draft'));
    await tester.pumpAndSettle();
    expect((savedDraft!['edges'] as List).single,
        containsPair('source', 'trigger'));
    expect((savedDraft!['edges'] as List).single,
        containsPair('target', 'action'));
    expect(
        (savedDraft!['nodes'] as List).map((node) => node['position']).toList(),
        [
          {'x': 100, 'y': 100},
          {'x': 420, 'y': 100},
          {'x': 760, 'y': 300}
        ]);
    await tester.tap(find.text('Connect to node'));
    await tester.pumpAndSettle();
    await tester.tap(find.textContaining('· next'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save draft'));
    await tester.pumpAndSettle();
    expect((savedDraft!['edges'] as List), hasLength(2));
    expect(
        (savedDraft!['edges'] as List).last,
        allOf(
            containsPair('source', 'trigger'), containsPair('target', 'next')));
  });
}
