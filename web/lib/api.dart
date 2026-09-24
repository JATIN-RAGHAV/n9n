import 'dart:convert';
import 'package:http/http.dart' as http;
import 'models.dart';

class ApiException implements Exception {
  ApiException(this.message, this.status);
  final String message;
  final int status;
  @override
  String toString() => message;
}

class Api {
  Api({http.Client? client}) : _client = client ?? http.Client();
  final http.Client _client;

  Future<Json> request(String method, String path, [Json? body]) async {
    final uri = Uri.parse('/api$path');
    final req = http.Request(method, uri);
    req.headers['Accept'] = 'application/json';
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final streamed = await _client.send(req);
    final response = await http.Response.fromStream(streamed);
    Json result;
    try {
      result = asJson(jsonDecode(response.body));
    } catch (_) {
      result = {};
    }
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw ApiException(
          '${result['error'] ?? result['message'] ?? 'Request failed (${response.statusCode})'}',
          response.statusCode);
    }
    return result;
  }

  Future<Json> me() => request('GET', '/auth/me');
  Future<Json> login(String email, String password) =>
      request('POST', '/auth/login', {'email': email, 'password': password});
  Future<Json> register(String email, String password) =>
      request('POST', '/auth/register', {'email': email, 'password': password});
  Future<void> logout() async {
    await request('POST', '/auth/logout');
  }

  Future<List<Json>> nodes() async =>
      asJsonList((await request('GET', '/nodes'))['nodes']);
  Future<List<Workflow>> workflows() async =>
      asJsonList((await request('GET', '/workflows'))['workflows'])
          .map(Workflow.fromJson)
          .toList();
  Future<Workflow> workflow(String id) async => Workflow.fromJson(
      asJson((await request('GET', '/workflows/$id'))['workflow']));
  Future<Workflow> createWorkflow(String name, WorkflowDraft draft) async =>
      Workflow.fromJson(asJson((await request('POST', '/workflows',
          {'name': name, 'draft': draft.toJson()}))['workflow']));
  Future<Workflow> saveWorkflow(Workflow workflow) async => Workflow.fromJson(
          asJson((await request('PUT', '/workflows/${workflow.id}', {
        'name': workflow.name,
        'draft': workflow.draft.toJson()
      }))['workflow']));
  Future<Workflow> publish(String id) async => Workflow.fromJson(
      asJson((await request('POST', '/workflows/$id/publish'))['workflow']));
  Future<Workflow> activate(String id, bool active) async =>
      Workflow.fromJson(asJson((await request(
          'POST', '/workflows/$id/activate', {'active': active}))['workflow']));
  Future<Json> run(String id, Json input) async => asJson(
      (await request('POST', '/workflows/$id/run', {'input': input}))['run']);
  Future<Json> testDraft(String id, Json input) async => asJson(
      (await request('POST', '/workflows/$id/test', {'input': input}))['run']);
  Future<
      List<Json>> runs({String? workflowId}) async => asJsonList((await request(
          'GET',
          '/runs${workflowId == null ? '' : '?workflow_id=${Uri.encodeQueryComponent(workflowId)}'}'))[
      'runs']);
  Future<Json> runDetail(String id) => request('GET', '/runs/$id');
  Future<void> cancelRun(String id) async {
    await request('POST', '/runs/$id/cancel');
  }

  Future<List<Json>> credentials() async =>
      asJsonList((await request('GET', '/credentials'))['credentials']);
  Future<Json> createCredential(String name, String kind, Json data) async =>
      asJson((await request('POST', '/credentials',
          {'name': name, 'kind': kind, 'data': data}))['credential']);
  Future<void> deleteCredential(String id) async {
    await request('DELETE', '/credentials/$id');
  }

  Future<String> googleStart() async =>
      '${(await request('GET', '/oauth/google/start'))['url'] ?? ''}';
}
