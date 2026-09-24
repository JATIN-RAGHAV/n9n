"""Exercise native bearer sessions through the running nginx API.

Run after `make up`: python3 tests/mobile_api_smoke.py
"""
import argparse
import secrets
from e2e_smoke import Client, node, edge, until


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--base', default='http://localhost:8080')
    args = parser.parse_args()
    client = Client(args.base)
    email = f'mobile-{secrets.token_hex(6)}@example.test'
    password = 'native-smoke-password-123'
    session = client.call('POST', '/api/auth/mobile/register', {'email': email, 'password': password})
    headers = {'Authorization': 'Bearer ' + session['token']}
    assert session['expires_at']
    assert client.call('GET', '/api/auth/me', headers=headers)['user']['email'] == email
    # Native login must not establish an ambient browser cookie session.
    client.call('GET', '/api/auth/me', expected=401)
    workflow = client.call('POST', '/api/workflows', {'name': 'Native session smoke', 'draft': {
        'nodes': [node('trigger', 'manual_trigger'), node('result', 'set_fields', {'fields': {'client': '{{input.client}}'}})],
        'edges': [edge('connection', 'trigger', 'result')],
    }}, headers=headers)['workflow']
    wid = workflow['id']
    client.call('POST', f'/api/workflows/{wid}/publish', headers=headers)
    run = client.call('POST', f'/api/workflows/{wid}/run', {'input': {'client': 'mobile'}}, headers=headers)['run']
    # The shared helper receives a bound bearer header on every polling call.
    class NativeClient:
        def call(self, method, path):
            return client.call(method, path, headers=headers)
    detail = until(NativeClient(), run['id'])
    assert detail['run']['status'] == 'succeeded', detail
    assert any((s.get('output') or {}).get('client') == 'mobile' for s in detail['steps'])
    client.call('POST', '/api/auth/logout', headers=headers)
    client.call('GET', '/api/auth/me', headers=headers, expected=401)
    login = client.call('POST', '/api/auth/mobile/login', {'email': email, 'password': password})
    fresh = {'Authorization': 'Bearer ' + login['token']}
    assert client.call('GET', f'/api/workflows/{wid}', headers=fresh)['workflow']['id'] == wid
    client.call('POST', '/api/auth/logout', headers=fresh)
    print('PASS: native registration/login, no ambient cookies, authenticated workflow execution, session revocation')


if __name__ == '__main__':
    main()
