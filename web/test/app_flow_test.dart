import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:n9n_web/api.dart';
import 'package:n9n_web/main.dart';

void main() {
  testWidgets('register, create a workflow, and open its editor', (tester) async {
    final calls = <String>[];
    final client = MockClient((request) async {
      calls.add('${request.method} ${request.url.path}');
      Object payload;
      int status = 200;
      switch ('${request.method} ${request.url.path}') {
        case 'GET /api/auth/me': status = 401; payload = {'error': 'authentication required'};
        case 'POST /api/auth/register': payload = {'user': {'id': 'user-1', 'email': 'me@example.com'}};
        case 'GET /api/workflows': payload = {'workflows': []};
        case 'GET /api/runs': payload = {'runs': []};
        case 'POST /api/workflows': payload = {'workflow': {'id': 'flow-1', 'name': 'Welcome flow', 'draft': {'nodes': [], 'edges': []}, 'active': false}};
        case 'GET /api/workflows/flow-1': payload = {'workflow': {'id': 'flow-1', 'name': 'Welcome flow', 'draft': {'nodes': [], 'edges': []}, 'active': false}};
        case 'GET /api/nodes': payload = {'nodes': []};
        case 'GET /api/credentials': payload = {'credentials': []};
        default: payload = {'runs': []};
      }
      return http.Response(jsonEncode(payload), status, headers: {'content-type': 'application/json'});
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
