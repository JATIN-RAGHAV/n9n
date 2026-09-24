import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'api.dart';
import 'models.dart';
import 'editor.dart';
import 'browser_navigation.dart';
import 'theme_storage.dart';

// Red accents stay consistent while each theme owns its surface and text colors.
const shell = Color(0xff090a0d);
const pine = Color(0xffe5484d);
const coral = Color(0xffff7378);

class AppPalette {
  const AppPalette(this.canvas, this.panel, this.panelRaised, this.fieldSurface,
      this.ink, this.muted, this.line, this.errorBg, this.accentText);
  final Color canvas,
      panel,
      panelRaised,
      fieldSurface,
      ink,
      muted,
      line,
      errorBg,
      accentText;

  static const dark = AppPalette(
      Color(0xff101217),
      Color(0xff1a1d24),
      Color(0xff242832),
      Color(0xff14171d),
      Color(0xfff5f2ef),
      Color(0xffadb3bd),
      Color(0xff343943),
      Color(0xff3d1a20),
      coral);
  static const light = AppPalette(
      Color(0xfff5f5f7),
      Color(0xffffffff),
      Color(0xffebeef2),
      Color(0xffffffff),
      Color(0xff1b2028),
      Color(0xff59616d),
      Color(0xffd5d9e0),
      Color(0xffffe5e7),
      Color(0xffb4232d));
}

extension AppColors on BuildContext {
  AppPalette get palette => Theme.of(this).brightness == Brightness.dark
      ? AppPalette.dark
      : AppPalette.light;
}

ThemeData appTheme(AppPalette palette, Brightness brightness) => ThemeData(
      useMaterial3: true,
      brightness: brightness,
      scaffoldBackgroundColor: palette.canvas,
      colorScheme: ColorScheme.fromSeed(
              seedColor: pine, brightness: brightness, surface: palette.panel)
          .copyWith(
              primary: brightness == Brightness.dark
                  ? pine
                  : const Color(0xffba2932),
              onPrimary: shell,
              secondary: coral,
              onSecondary: shell,
              onSurface: palette.ink),
      dialogTheme: DialogThemeData(backgroundColor: palette.panelRaised),
      bottomSheetTheme:
          BottomSheetThemeData(backgroundColor: palette.panelRaised),
      cardTheme: CardThemeData(color: palette.panel),
      popupMenuTheme: PopupMenuThemeData(color: palette.panelRaised),
      dividerTheme: DividerThemeData(color: palette.line),
      filledButtonTheme: FilledButtonThemeData(
          style: FilledButton.styleFrom(
              backgroundColor: pine, foregroundColor: shell)),
      fontFamily: 'Roboto',
      textTheme: TextTheme(
          headlineLarge: TextStyle(
              fontSize: 34,
              fontWeight: FontWeight.w800,
              color: palette.ink,
              letterSpacing: -1.3),
          headlineMedium: TextStyle(
              fontSize: 24,
              fontWeight: FontWeight.w800,
              color: palette.ink,
              letterSpacing: -.6),
          titleLarge:
              TextStyle(fontWeight: FontWeight.w700, color: palette.ink),
          bodyMedium: TextStyle(color: palette.ink)),
      inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: palette.fieldSurface,
          hintStyle: TextStyle(color: palette.muted),
          labelStyle: TextStyle(color: palette.muted),
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(10)),
          enabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(10),
              borderSide: BorderSide(color: palette.line)),
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
          focusedBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(10),
              borderSide: BorderSide(color: pine, width: 2))),
    );

void main() {
  final initialUri = Uri.base;
  runApp(N9nApp(initialUri: initialUri));
}

class N9nApp extends StatefulWidget {
  const N9nApp({super.key, this.api, this.initialUri});
  final Api? api;
  final Uri? initialUri;
  @override
  State<N9nApp> createState() => _N9nAppState();
}

class _N9nAppState extends State<N9nApp> {
  late final Api api = widget.api ?? Api();
  late bool darkMode = readThemePreference() != 'light';
  bool loading = true;
  Json? user;

  @override
  void initState() {
    super.initState();
    api.me().then((result) {
      if (mounted) {
        setState(() {
          user = asJson(result['user']);
          loading = false;
        });
      }
    }).catchError((_) {
      if (mounted) setState(() => loading = false);
    });
  }

  void authenticated(Json value) => setState(() => user = value);
  void toggleTheme() {
    setState(() => darkMode = !darkMode);
    saveThemePreference(darkMode ? 'dark' : 'light');
  }

  Future<void> signOut() async {
    await api.logout();
    if (mounted) setState(() => user = null);
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'n9n',
      debugShowCheckedModeBanner: false,
      theme: appTheme(darkMode ? AppPalette.dark : AppPalette.light,
          darkMode ? Brightness.dark : Brightness.light),
      home: loading
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : user == null
              ? AuthScreen(
                  api: api,
                  onAuthenticated: authenticated,
                  darkMode: darkMode,
                  onToggleTheme: toggleTheme)
              : AppShell(
                  api: api,
                  user: user!,
                  onSignOut: signOut,
                  darkMode: darkMode,
                  onToggleTheme: toggleTheme,
                  initialUri: widget.initialUri ?? Uri.base),
    );
  }
}

class AuthScreen extends StatefulWidget {
  const AuthScreen(
      {super.key,
      required this.api,
      required this.onAuthenticated,
      required this.darkMode,
      required this.onToggleTheme});
  final Api api;
  final ValueChanged<Json> onAuthenticated;
  final bool darkMode;
  final VoidCallback onToggleTheme;
  @override
  State<AuthScreen> createState() => _AuthScreenState();
}

class _AuthScreenState extends State<AuthScreen> {
  final email = TextEditingController();
  final password = TextEditingController();
  bool register = false, busy = false;
  String? error;
  Future<void> submit() async {
    if (email.text.trim().isEmpty || password.text.isEmpty) {
      setState(() => error = 'Enter an email and password.');
      return;
    }
    setState(() {
      busy = true;
      error = null;
    });
    try {
      final result = register
          ? await widget.api.register(email.text.trim(), password.text)
          : await widget.api.login(email.text.trim(), password.text);
      widget.onAuthenticated(asJson(result['user']));
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  void dispose() {
    email.dispose();
    password.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
          body: Row(children: [
        if (MediaQuery.sizeOf(context).width > 850)
          Expanded(
              child: Container(
                  color: shell,
                  padding: const EdgeInsets.all(64),
                  child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Brand(light: true),
                        const Spacer(),
                        Text('Give your work\na better rhythm.',
                            style: TextStyle(
                                color: Colors.white,
                                fontSize: 56,
                                height: 1.08,
                                fontWeight: FontWeight.w800,
                                letterSpacing: -2)),
                        const SizedBox(height: 22),
                        Text(
                            'Design, run, and understand every automation in one calm workspace.',
                            style: TextStyle(
                                color: Colors.white.withValues(alpha: .72),
                                fontSize: 18)),
                        const Spacer(),
                        Text('VISUAL WORKFLOWS  /  CLEAR EXECUTION',
                            style: TextStyle(
                                color: coral,
                                fontSize: 12,
                                fontWeight: FontWeight.w800,
                                letterSpacing: 2)),
                      ]))),
        Expanded(
            child: Center(
                child: SingleChildScrollView(
                    child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 430),
                        child: Padding(
                            padding: const EdgeInsets.all(30),
                            child: Column(
                                mainAxisSize: MainAxisSize.min,
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Align(
                                      alignment: Alignment.centerRight,
                                      child: ThemeToggle(
                                          darkMode: widget.darkMode,
                                          onPressed: widget.onToggleTheme)),
                                  if (MediaQuery.sizeOf(context).width <=
                                      850) ...[
                                    const Brand(),
                                    const SizedBox(height: 40)
                                  ],
                                  Text(
                                      register
                                          ? 'Create your workspace'
                                          : 'Welcome back',
                                      style: Theme.of(context)
                                          .textTheme
                                          .headlineLarge),
                                  const SizedBox(height: 8),
                                  Text(
                                      register
                                          ? 'Start building useful workflows.'
                                          : 'Sign in to continue your work.',
                                      style: TextStyle(
                                          color: context.palette.muted)),
                                  const SizedBox(height: 32),
                                  Text('EMAIL', style: fieldLabel),
                                  const SizedBox(height: 8),
                                  TextField(
                                      controller: email,
                                      keyboardType: TextInputType.emailAddress,
                                      decoration: const InputDecoration(
                                          hintText: 'you@company.com')),
                                  const SizedBox(height: 20),
                                  Text('PASSWORD', style: fieldLabel),
                                  const SizedBox(height: 8),
                                  TextField(
                                      controller: password,
                                      obscureText: true,
                                      onSubmitted: (_) => submit(),
                                      decoration: const InputDecoration(
                                          hintText: 'Enter your password')),
                                  if (error != null) ...[
                                    const SizedBox(height: 16),
                                    Text(error!,
                                        style: TextStyle(
                                            color: context.palette.accentText))
                                  ],
                                  const SizedBox(height: 24),
                                  SizedBox(
                                      width: double.infinity,
                                      child: FilledButton(
                                          onPressed: busy ? null : submit,
                                          style: FilledButton.styleFrom(
                                              backgroundColor: pine,
                                              padding:
                                                  const EdgeInsets.all(17)),
                                          child: Text(busy
                                              ? 'Please wait…'
                                              : register
                                                  ? 'Create account'
                                                  : 'Sign in'))),
                                  const SizedBox(height: 12),
                                  Center(
                                      child: TextButton(
                                          onPressed: () => setState(() {
                                                register = !register;
                                                error = null;
                                              }),
                                          child: Text(register
                                              ? 'Already have an account? Sign in'
                                              : 'New here? Create an account'))),
                                ])))))),
      ]));
}

const fieldLabel =
    TextStyle(fontSize: 11, letterSpacing: 1.3, fontWeight: FontWeight.w800);
void disposeDialogController(TextEditingController controller) =>
    Future<void>.delayed(const Duration(milliseconds: 400), controller.dispose);

class Brand extends StatelessWidget {
  const Brand({super.key, this.light = false});
  final bool light;
  @override
  Widget build(BuildContext context) =>
      Row(mainAxisSize: MainAxisSize.min, children: [
        Container(
            width: 32,
            height: 32,
            decoration: BoxDecoration(
                color: coral, borderRadius: BorderRadius.circular(9)),
            child: Icon(Icons.account_tree_rounded, color: shell, size: 22)),
        const SizedBox(width: 10),
        Text('n9n',
            style: TextStyle(
                color: light ? Colors.white : context.palette.ink,
                fontSize: 25,
                fontWeight: FontWeight.w900,
                letterSpacing: -1.5)),
      ]);
}

class ThemeToggle extends StatelessWidget {
  const ThemeToggle(
      {super.key,
      required this.darkMode,
      required this.onPressed,
      this.onDarkHeader = false});
  final bool darkMode;
  final VoidCallback onPressed;
  final bool onDarkHeader;

  @override
  Widget build(BuildContext context) => IconButton(
      tooltip: darkMode ? 'Switch to light theme' : 'Switch to dark theme',
      onPressed: onPressed,
      icon: Icon(
          darkMode ? Icons.light_mode_outlined : Icons.dark_mode_outlined,
          color: onDarkHeader ? Colors.white : context.palette.ink));
}

class AppShell extends StatefulWidget {
  const AppShell(
      {super.key,
      required this.api,
      required this.user,
      required this.onSignOut,
      required this.darkMode,
      required this.onToggleTheme,
      required this.initialUri});
  final Api api;
  final Json user;
  final Future<void> Function() onSignOut;
  final bool darkMode;
  final VoidCallback onToggleTheme;
  final Uri initialUri;
  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> with WidgetsBindingObserver {
  late String route = _pathFromUri(widget.initialUri);
  static String _pathFromUri(Uri uri) {
    final fragment = uri.fragment;
    if (fragment.startsWith('/')) {
      return Uri.tryParse(fragment)?.path ?? '/workflows';
    }
    return uri.path.isEmpty ? '/workflows' : uri.path;
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final fragment = widget.initialUri.fragment;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        SystemNavigator.routeInformationUpdated(
            uri: Uri.parse(route), replace: true);
      }
    });
    if (fragment.startsWith('/credentials?')) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        final query = Uri.tryParse(fragment)?.queryParameters ?? {};
        if (query['connected'] == 'gmail') {
          showSuccess(context, 'Gmail connected');
        }
        if (query.containsKey('error')) {
          showError(context, 'Gmail connection failed: ${query['error']}');
        }
        SystemNavigator.routeInformationUpdated(uri: Uri.parse('/credentials'));
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Future<bool> didPushRouteInformation(
      RouteInformation routeInformation) async {
    if (mounted) setState(() => route = _pathFromUri(routeInformation.uri));
    return true;
  }

  @override
  Future<bool> didPopRoute() async {
    if (mounted) setState(() => route = _pathFromUri(Uri.base));
    return true;
  }

  void go(String path) {
    setState(() => route = path);
    SystemNavigator.routeInformationUpdated(uri: Uri.parse(path));
  }

  @override
  Widget build(BuildContext context) {
    final width = MediaQuery.sizeOf(context).width;
    final editorRoute =
        route.startsWith('/workflows/') && route != '/workflows';
    Widget page;
    if (editorRoute) {
      page = EditorScreen(
          key: ValueKey(route),
          api: widget.api,
          workflowId: route.split('/').last,
          onNavigate: go);
    } else if (route.startsWith('/credentials')) {
      page = CredentialScreen(api: widget.api);
    } else if (route.startsWith('/runs/')) {
      page = RunDetailScreen(
          key: ValueKey(route),
          api: widget.api,
          runId: route.split('/').last,
          onBack: () => go('/workflows'));
    } else {
      page =
          WorkflowHome(api: widget.api, onOpen: (id) => go('/workflows/$id'));
    }
    return Scaffold(
        body: Column(children: [
      Container(
          height: 68,
          color: shell,
          padding: EdgeInsets.symmetric(horizontal: width < 700 ? 16 : 32),
          child: Row(children: [
            const Brand(light: true),
            if (width < 600) ...[
              const Spacer(),
              ThemeToggle(
                  darkMode: widget.darkMode,
                  onPressed: widget.onToggleTheme,
                  onDarkHeader: true),
              PopupMenuButton<String>(
                tooltip: 'Open navigation menu',
                icon: Icon(Icons.menu, color: Colors.white),
                onSelected: (value) {
                  if (value == 'logout') {
                    widget.onSignOut();
                  } else {
                    go(value);
                  }
                },
                itemBuilder: (_) => const [
                  PopupMenuItem(value: '/workflows', child: Text('Workflows')),
                  PopupMenuItem(
                      value: '/credentials', child: Text('Credentials')),
                  PopupMenuDivider(),
                  PopupMenuItem(value: 'logout', child: Text('Sign out')),
                ],
              ),
            ] else ...[
              const SizedBox(width: 38),
              _NavItem(
                  'Workflows',
                  Icons.hub_outlined,
                  route.startsWith('/workflows') || route == '/',
                  () => go('/workflows')),
              const SizedBox(width: 10),
              _NavItem('Credentials', Icons.key_rounded,
                  route.startsWith('/credentials'), () => go('/credentials')),
              const Spacer(),
              if (width > 950)
                Text('${widget.user['email'] ?? ''}',
                    style: TextStyle(color: Colors.white70, fontSize: 13)),
              const SizedBox(width: 12),
              ThemeToggle(
                  darkMode: widget.darkMode,
                  onPressed: widget.onToggleTheme,
                  onDarkHeader: true),
              IconButton(
                  tooltip: 'Sign out',
                  onPressed: widget.onSignOut,
                  icon: Icon(Icons.logout, color: Colors.white70)),
            ],
          ])),
      Expanded(child: page),
    ]));
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem(this.title, this.icon, this.selected, this.onTap);
  final String title;
  final IconData icon;
  final bool selected;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => TextButton.icon(
      onPressed: onTap,
      style: TextButton.styleFrom(
          foregroundColor: selected ? coral : Colors.white70),
      icon: Icon(icon, size: 18),
      label: Text(title));
}

class WorkflowHome extends StatefulWidget {
  const WorkflowHome({super.key, required this.api, required this.onOpen});
  final Api api;
  final ValueChanged<String> onOpen;
  @override
  State<WorkflowHome> createState() => _WorkflowHomeState();
}

class _WorkflowHomeState extends State<WorkflowHome> {
  List<Workflow>? workflows;
  List<Json>? runs;
  String? error;
  bool creating = false;
  @override
  void initState() {
    super.initState();
    refresh();
  }

  Future<void> refresh() async {
    try {
      final results =
          await Future.wait([widget.api.workflows(), widget.api.runs()]);
      if (mounted) {
        setState(() {
          workflows = results[0] as List<Workflow>;
          runs = results[1] as List<Json>;
          error = null;
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    }
  }

  Future<void> create() async {
    final controller = TextEditingController();
    final name = await showDialog<String>(
        context: context,
        builder: (context) => AlertDialog(
                title: Text('New workflow'),
                content: TextField(
                    controller: controller,
                    autofocus: true,
                    decoration: const InputDecoration(
                        labelText: 'Workflow name', hintText: 'Lead follow-up'),
                    onSubmitted: (_) =>
                        Navigator.pop(context, controller.text.trim())),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(context),
                      child: Text('Cancel')),
                  FilledButton(
                      onPressed: () =>
                          Navigator.pop(context, controller.text.trim()),
                      child: Text('Create'))
                ]));
    disposeDialogController(controller);
    if (name == null || name.isEmpty) return;
    setState(() => creating = true);
    try {
      final workflow = await widget.api.createWorkflow(name, WorkflowDraft());
      widget.onOpen(workflow.id);
    } catch (e) {
      if (mounted) showError(context, '$e');
    } finally {
      if (mounted) setState(() => creating = false);
    }
  }

  @override
  Widget build(BuildContext context) => RefreshIndicator(
      onRefresh: refresh,
      child: ListView(
          padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 32),
          children: [
            Wrap(
                alignment: WrapAlignment.spaceBetween,
                crossAxisAlignment: WrapCrossAlignment.center,
                spacing: 20,
                runSpacing: 20,
                children: [
                  Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('YOUR WORKSPACE', style: fieldLabel),
                        const SizedBox(height: 8),
                        Text('Workflows',
                            style: Theme.of(context).textTheme.headlineLarge),
                        const SizedBox(height: 5),
                        Text('Build the flow. Know what happened.',
                            style: TextStyle(color: context.palette.muted))
                      ]),
                  FilledButton.icon(
                      onPressed: creating ? null : create,
                      style: FilledButton.styleFrom(
                          backgroundColor: pine,
                          padding: const EdgeInsets.symmetric(
                              horizontal: 20, vertical: 17)),
                      icon: Icon(Icons.add),
                      label: Text('New workflow')),
                ]),
            const SizedBox(height: 30),
            if (error != null) ErrorCard(error!, refresh),
            if (workflows == null && error == null)
              const Center(
                  child: Padding(
                      padding: EdgeInsets.all(50),
                      child: CircularProgressIndicator())),
            if (workflows?.isEmpty == true)
              EmptyCard(
                  icon: Icons.hub_outlined,
                  title: 'Your first flow starts here',
                  detail:
                      'Create a workflow, add a trigger, then connect actions on the canvas.',
                  action: 'Create workflow',
                  onAction: create),
            if (workflows?.isNotEmpty == true) ...[
              Text('ALL WORKFLOWS', style: fieldLabel),
              const SizedBox(height: 12),
              ...workflows!.map((workflow) => Card(
                  color: context.palette.panel,
                  elevation: 0,
                  margin: const EdgeInsets.only(bottom: 12),
                  shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(14),
                      side: BorderSide(color: context.palette.line)),
                  child: InkWell(
                      borderRadius: BorderRadius.circular(14),
                      onTap: () => widget.onOpen(workflow.id),
                      child: Padding(
                          padding: const EdgeInsets.all(20),
                          child: Row(children: [
                            Container(
                                width: 46,
                                height: 46,
                                decoration: BoxDecoration(
                                    color: context.palette.panelRaised,
                                    borderRadius: BorderRadius.circular(12)),
                                child: Icon(Icons.account_tree_outlined,
                                    color: pine)),
                            const SizedBox(width: 18),
                            Expanded(
                                child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                  Text(workflow.name,
                                      style: TextStyle(
                                          fontWeight: FontWeight.w800,
                                          fontSize: 17)),
                                  const SizedBox(height: 5),
                                  Text(
                                      '${workflow.draft.nodes.length} nodes · ${workflow.draft.edges.length} connections',
                                      style: TextStyle(
                                          color: context.palette.muted,
                                          fontSize: 13))
                                ])),
                            StatusPill(
                                workflow.active
                                    ? 'Active'
                                    : workflow.publishedVersion != null
                                        ? 'Published'
                                        : 'Draft',
                                active: workflow.active),
                            const SizedBox(width: 14),
                            Icon(Icons.arrow_forward_ios,
                                size: 14, color: context.palette.muted),
                          ]))))),
            ],
            const SizedBox(height: 30),
            Text('RECENT RUNS', style: fieldLabel),
            const SizedBox(height: 12),
            if (runs?.isEmpty == true)
              Text(
                  'No runs yet. Publish a workflow and run it from the editor.',
                  style: TextStyle(color: context.palette.muted)),
            ...?runs?.take(6).map((run) => ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(
                      run['status'] == 'succeeded'
                          ? Icons.check_circle
                          : run['status'] == 'failed'
                              ? Icons.error
                              : Icons.sync,
                      color: run['status'] == 'succeeded' ? pine : coral),
                  title: Text(
                      '${run['workflow_name'] ?? run['workflow_id'] ?? 'Workflow'}'),
                  subtitle: Text(
                      '${run['status'] ?? 'queued'} · ${run['created_at'] ?? ''}'),
                  trailing: Icon(Icons.chevron_right),
                  onTap: () => Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => RunDetailScreen(
                            api: widget.api,
                            runId: '${run['id']}',
                            onBack: () => Navigator.pop(context),
                          ))),
                )),
          ]));
}

class CredentialScreen extends StatefulWidget {
  const CredentialScreen({super.key, required this.api});
  final Api api;
  @override
  State<CredentialScreen> createState() => _CredentialScreenState();
}

class _CredentialScreenState extends State<CredentialScreen> {
  List<Json>? credentials;
  String? error;
  @override
  void initState() {
    super.initState();
    refresh();
  }

  Future<void> refresh() async {
    try {
      final list = await widget.api.credentials();
      if (mounted) {
        setState(() {
          credentials = list;
          error = null;
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    }
  }

  Future<void> connect() async {
    try {
      final url = await widget.api.googleStart();
      final target = Uri.tryParse(url);
      if (target == null ||
          target.scheme != 'https' ||
          target.host != 'accounts.google.com') {
        throw ApiException('Invalid Google authorization URL.', 500);
      }
      openExternalUrl(url);
    } catch (e) {
      if (mounted) showError(context, '$e');
    }
  }

  Future<void> add() async {
    final name = TextEditingController(),
        clientId = TextEditingController(),
        clientSecret = TextEditingController(),
        refreshToken = TextEditingController();
    bool busy = false;
    String? localError;
    await showDialog<void>(
        context: context,
        builder: (context) => StatefulBuilder(
            builder: (context, setDialog) => AlertDialog(
                    title: Text('Add Gmail credential'),
                    content: SizedBox(
                        width: 430,
                        child: SingleChildScrollView(
                            child: Column(
                                mainAxisSize: MainAxisSize.min,
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                              Text(
                                  'Use a Google OAuth client with Gmail access. Generate a refresh token for the mailbox, then save the three values below. Secrets are encrypted on the server.',
                                  style: TextStyle(
                                      fontSize: 13,
                                      color: context.palette.muted)),
                              const SizedBox(height: 20),
                              TextField(
                                  controller: name,
                                  decoration: const InputDecoration(
                                      labelText: 'Name',
                                      hintText: 'Team inbox')),
                              const SizedBox(height: 12),
                              TextField(
                                  controller: clientId,
                                  decoration: const InputDecoration(
                                      labelText: 'OAuth client ID')),
                              const SizedBox(height: 12),
                              TextField(
                                  controller: clientSecret,
                                  obscureText: true,
                                  decoration: const InputDecoration(
                                      labelText: 'OAuth client secret')),
                              const SizedBox(height: 12),
                              TextField(
                                  controller: refreshToken,
                                  obscureText: true,
                                  decoration: const InputDecoration(
                                      labelText: 'Refresh token')),
                              if (localError != null) ...[
                                const SizedBox(height: 10),
                                Text(localError!,
                                    style: TextStyle(
                                        color: context.palette.accentText))
                              ],
                            ]))),
                    actions: [
                      TextButton(
                          onPressed: busy ? null : () => Navigator.pop(context),
                          child: Text('Cancel')),
                      FilledButton(
                          onPressed: busy
                              ? null
                              : () async {
                                  if ([
                                    name.text,
                                    clientId.text,
                                    clientSecret.text,
                                    refreshToken.text
                                  ].any((v) => v.trim().isEmpty)) {
                                    setDialog(() =>
                                        localError = 'Fill in every field.');
                                    return;
                                  }
                                  setDialog(() => busy = true);
                                  try {
                                    await widget.api.createCredential(
                                        name.text.trim(), 'gmail_oauth', {
                                      'client_id': clientId.text.trim(),
                                      'client_secret': clientSecret.text.trim(),
                                      'refresh_token': refreshToken.text.trim()
                                    });
                                    if (context.mounted) Navigator.pop(context);
                                    refresh();
                                  } catch (e) {
                                    setDialog(() {
                                      localError = '$e';
                                      busy = false;
                                    });
                                  }
                                },
                          child: Text(busy ? 'Saving…' : 'Save credential'))
                    ])));
    for (final controller in [name, clientId, clientSecret, refreshToken]) {
      disposeDialogController(controller);
    }
  }

  Future<void> remove(Json credential) async {
    final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
                title: Text('Delete credential?'),
                content: Text(
                    'Delete ${credential['name']}? Workflows using it will need a new credential.'),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(context, false),
                      child: Text('Cancel')),
                  FilledButton(
                      onPressed: () => Navigator.pop(context, true),
                      child: Text('Delete'))
                ]));
    if (confirmed != true) return;
    try {
      await widget.api.deleteCredential('${credential['id']}');
      refresh();
    } catch (e) {
      if (mounted) showError(context, '$e');
    }
  }

  @override
  Widget build(BuildContext context) =>
      ListView(padding: const EdgeInsets.all(32), children: [
        Wrap(
            alignment: WrapAlignment.spaceBetween,
            crossAxisAlignment: WrapCrossAlignment.center,
            runSpacing: 16,
            children: [
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text('SECURE CONNECTIONS', style: fieldLabel),
                const SizedBox(height: 8),
                Text('Credentials',
                    style: Theme.of(context).textTheme.headlineLarge),
                const SizedBox(height: 5),
                Text('Connect inboxes used by email triggers and actions.')
              ]),
              FilledButton.icon(
                  onPressed: connect,
                  icon: Icon(Icons.add),
                  label: Text('Connect Gmail'),
                  style: FilledButton.styleFrom(backgroundColor: pine))
            ]),
        const SizedBox(height: 10),
        Align(
            alignment: Alignment.centerRight,
            child: TextButton(
                onPressed: add,
                child: Text('Add OAuth refresh token manually'))),
        const SizedBox(height: 28),
        if (error != null) ErrorCard(error!, refresh),
        if (credentials == null && error == null)
          const Center(child: CircularProgressIndicator()),
        if (credentials?.isEmpty == true)
          EmptyCard(
              icon: Icons.key_outlined,
              title: 'No credentials yet',
              detail: 'Connect a Gmail account to receive or send email.',
              action: 'Connect Gmail',
              onAction: connect),
        ...?credentials?.map((item) => Card(
            color: context.palette.panel,
            elevation: 0,
            child: ListTile(
                contentPadding:
                    const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                leading: Icon(Icons.mail_outline, color: pine),
                title: Text('${item['name']}',
                    style: TextStyle(fontWeight: FontWeight.w700)),
                subtitle: Text(
                    '${item['kind'] ?? 'gmail_oauth'} · Created ${item['created_at'] ?? ''}'),
                trailing: IconButton(
                    tooltip: 'Delete credential',
                    icon: Icon(Icons.delete_outline),
                    onPressed: () => remove(item))))),
      ]);
}

class RunDetailScreen extends StatefulWidget {
  const RunDetailScreen(
      {super.key,
      required this.api,
      required this.runId,
      required this.onBack});
  final Api api;
  final String runId;
  final VoidCallback onBack;
  @override
  State<RunDetailScreen> createState() => _RunDetailScreenState();
}

class _RunDetailScreenState extends State<RunDetailScreen> {
  Json? detail;
  String? error;
  Timer? polling;
  bool refreshing = false;
  final nodeTypes = <String, String>{};
  @override
  void initState() {
    super.initState();
    refresh();
  }

  @override
  void dispose() {
    polling?.cancel();
    super.dispose();
  }

  Future<void> refresh() async {
    if (refreshing) return;
    refreshing = true;
    try {
      final data = await widget.api.runDetail(widget.runId);
      if (!mounted) return;
      final run = asJson(data['run']);
      final snapshotTypes = asJson(data['node_types']);
      if (snapshotTypes.isNotEmpty) {
        nodeTypes
          ..clear()
          ..addAll(snapshotTypes.map((key, value) => MapEntry(key, '$value')));
      } else if (nodeTypes.isEmpty && run['workflow_id'] != null) {
        try {
          final workflow = await widget.api.workflow('${run['workflow_id']}');
          for (final node in workflow.draft.nodes) {
            nodeTypes[node.id] = node.type;
          }
        } catch (_) {/* The recorded node IDs still identify the steps. */}
      }
      if (mounted) {
        setState(() {
          detail = data;
          error = null;
        });
        if (run['status'] == 'queued' || run['status'] == 'running') {
          polling ??=
              Timer.periodic(const Duration(seconds: 2), (_) => refresh());
        } else {
          polling?.cancel();
          polling = null;
        }
      }
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    } finally {
      refreshing = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final run = asJson(detail?['run']);
    final latestSteps = <String, Json>{};
    for (final step in asJsonList(detail?['steps'])) {
      latestSteps['${step['node_id']}:${step['attempt'] ?? 1}'] = step;
    }
    final steps = latestSteps.values.toList();
    return ListView(padding: const EdgeInsets.all(32), children: [
      Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
              onPressed: widget.onBack,
              icon: Icon(Icons.arrow_back),
              label: Text('Back'))),
      const SizedBox(height: 12),
      Wrap(
          alignment: WrapAlignment.spaceBetween,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text('EXECUTION INSPECTOR', style: fieldLabel),
              const SizedBox(height: 8),
              Text(
                  'Run ${widget.runId.length > 8 ? widget.runId.substring(0, 8) : widget.runId}',
                  style: Theme.of(context).textTheme.headlineLarge)
            ]),
            Row(mainAxisSize: MainAxisSize.min, children: [
              OutlinedButton.icon(
                  onPressed: refresh,
                  icon: Icon(Icons.refresh),
                  label: Text('Refresh')),
              const SizedBox(width: 8),
              if (run['status'] == 'queued' || run['status'] == 'running')
                OutlinedButton.icon(
                    onPressed: () async {
                      try {
                        await widget.api.cancelRun(widget.runId);
                        refresh();
                      } catch (e) {
                        if (context.mounted) showError(context, '$e');
                      }
                    },
                    icon: Icon(Icons.stop_circle_outlined),
                    label: Text('Cancel run'))
            ])
          ]),
      const SizedBox(height: 24),
      if (error != null) ErrorCard(error!, refresh),
      if (detail == null && error == null)
        const Center(child: CircularProgressIndicator()),
      if (detail != null) ...[
        Card(
            color: context.palette.panel,
            elevation: 0,
            child: Padding(
                padding: const EdgeInsets.all(22),
                child: Wrap(spacing: 28, runSpacing: 15, children: [
                  InfoDatum('STATUS', '${run['status'] ?? ''}'),
                  InfoDatum('MODE', run['test'] == true ? 'DRAFT TEST' : 'PUBLISHED RUN'),
                  InfoDatum('CREATED', '${run['created_at'] ?? ''}'),
                  InfoDatum('UPDATED', '${run['updated_at'] ?? ''}')
                ]))),
        if ('${run['error'] ?? ''}'.isNotEmpty)
          Padding(
              padding: const EdgeInsets.only(top: 12),
              child: SelectableText('Error: ${run['error']}',
                  style: TextStyle(color: context.palette.accentText))),
        const SizedBox(height: 22),
        Text('STEP BY STEP', style: fieldLabel),
        const SizedBox(height: 12),
        if (steps.isEmpty)
          Text(run['status'] == 'queued' || run['status'] == 'running'
              ? 'Waiting for the worker to record steps…'
              : 'No steps were recorded.'),
        ...steps.asMap().entries.map((entry) {
          final step = entry.value;
          return Card(
              color: context.palette.panel,
              elevation: 0,
              margin: const EdgeInsets.only(bottom: 12),
              child: ExpansionTile(
                  initiallyExpanded: step['error'] != null,
                  leading: CircleAvatar(
                      backgroundColor: step['status'] == 'failed'
                          ? context.palette.errorBg
                          : context.palette.panelRaised,
                      child: Text('${entry.key + 1}',
                          style: TextStyle(color: context.palette.ink))),
                  title: Text(
                      nodeTypes.containsKey('${step['node_id']}')
                          ? '${nodeLabel(nodeTypes['${step['node_id']}']!)} · ${step['node_id']}'
                          : '${step['node_id'] ?? 'Step'}',
                      style: TextStyle(fontWeight: FontWeight.w700)),
                  subtitle: Text(
                      '${step['status'] == 'running' && run['status'] != 'running' ? 'interrupted' : step['status'] ?? 'completed'} · attempt ${step['attempt'] ?? 1}'),
                  children: [
                    Padding(
                        padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
                        child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              if ('${step['error'] ?? ''}'.isNotEmpty)
                                Text('${step['error']}',
                                    style: TextStyle(
                                        color: context.palette.accentText)),
                              JsonBlock(
                                  'INPUT', step['input'] ?? step['input_json']),
                              JsonBlock('OUTPUT',
                                  step['output'] ?? step['output_json'])
                            ]))
                  ]));
        }),
      ],
    ]);
  }
}

class InfoDatum extends StatelessWidget {
  const InfoDatum(this.label, this.value, {super.key});
  final String label, value;
  @override
  Widget build(BuildContext context) =>
      Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(label, style: fieldLabel),
        const SizedBox(height: 7),
        Text(value, style: TextStyle(fontWeight: FontWeight.w700))
      ]);
}

class JsonBlock extends StatelessWidget {
  const JsonBlock(this.label, this.value, {super.key});
  final String label;
  final Object? value;
  @override
  Widget build(BuildContext context) {
    String pretty;
    try {
      pretty = const JsonEncoder.withIndent('  ').convert(value);
    } catch (_) {
      pretty = '$value';
    }
    return Padding(
        padding: const EdgeInsets.only(top: 14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(label, style: fieldLabel),
          const SizedBox(height: 6),
          Container(
              width: double.infinity,
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(
                  color: context.palette.fieldSurface,
                  borderRadius: BorderRadius.circular(9)),
              child: SelectableText(pretty,
                  style: TextStyle(fontFamily: 'monospace', fontSize: 12)))
        ]));
  }
}

class StatusPill extends StatelessWidget {
  const StatusPill(this.label, {super.key, this.active = false});
  final String label;
  final bool active;
  @override
  Widget build(BuildContext context) => Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
          color: active ? context.palette.errorBg : context.palette.panelRaised,
          borderRadius: BorderRadius.circular(20)),
      child: Text(label.toUpperCase(),
          style: TextStyle(
              color:
                  active ? context.palette.accentText : context.palette.muted,
              fontSize: 10,
              fontWeight: FontWeight.w800,
              letterSpacing: 1)));
}

class EmptyCard extends StatelessWidget {
  const EmptyCard(
      {super.key,
      required this.icon,
      required this.title,
      required this.detail,
      required this.action,
      required this.onAction});
  final IconData icon;
  final String title, detail, action;
  final VoidCallback onAction;
  @override
  Widget build(BuildContext context) => Container(
      padding: const EdgeInsets.all(44),
      decoration: BoxDecoration(
          color: context.palette.panel,
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: context.palette.line)),
      child: Column(children: [
        Icon(icon, size: 45, color: pine),
        const SizedBox(height: 15),
        Text(title, style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 6),
        Text(detail, textAlign: TextAlign.center),
        const SizedBox(height: 20),
        OutlinedButton(onPressed: onAction, child: Text(action))
      ]));
}

class ErrorCard extends StatelessWidget {
  const ErrorCard(this.message, this.retry, {super.key});
  final String message;
  final VoidCallback retry;
  @override
  Widget build(BuildContext context) => Card(
      color: context.palette.errorBg,
      shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: pine)),
      child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(children: [
            Expanded(child: Text('Error: $message')),
            TextButton(onPressed: retry, child: Text('Retry'))
          ])));
}

void showError(BuildContext context, String message) =>
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('Error: $message'),
        backgroundColor: context.palette.errorBg));
void showSuccess(BuildContext context, String message) =>
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(message), backgroundColor: context.palette.panelRaised));
