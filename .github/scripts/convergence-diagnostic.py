"""Bounded exact-tree diagnostics. No retry-to-green and no release-gate credit."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import signal
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
SEEDS = ('1783794398036981000', '20260712', '424242')
FAMILIES = ('windows-root', 'windows-nested', 'windows-smoke',
            'linux-nested', 'linux-root', 'macos-control')
# Expanded native reader inventory, including the pending/error/deadline and
# supervisor-accounting boundaries. A total alone cannot prove these ran.
READER_CHECKS = (
    'unlocked PID control', 'old RED and shared writer GREEN',
    'replacement returns fresh contents', 'delete-access handle is shared',
    'missing PID never becomes fresh',
    *(f'reject PID [{value}]' for value in ('111', '0', '-2', 'not-a-pid', '222 extra', '')),
    'single-attempt shared read still reports exclusive lock',
    'bounded PID observer treats sharing conflict as pending',
    'permanent invalid path remains an error',
    'verified daemon and matching artifact control', 'runtime reader coexists with writer',
    'persistent runtime lock fails at the original deadline',
    'supervisor accounting accepts one crash and rejects absorbed failure',
    *(f'reject {value}' for value in ('not running', 'old PID', 'unowned daemon',
      'no restart policy', 'wrong autostart scope', 'wrong runtime PID',
      'stale artifact', 'future artifact', 'missing artifact')),
    'malformed artifact is an error',
)


def write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def inputs():
    sha = os.environ['DIAGNOSTIC_SHA']
    reps = os.environ['DIAGNOSTIC_REPETITIONS']
    if not re.fullmatch('[0-9a-f]{40}', sha) or not re.fullmatch('(?:[1-9]|10)', reps):
        raise ValueError('need exact lowercase commit SHA and repetitions in 1..10')
    return sha, int(reps)


def validate():
    sha, _ = inputs()
    actual = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if actual != sha or os.environ.get('GITHUB_SHA') != sha:
        raise ValueError('checkout, dispatch workflow and requested SHA must be identical')
    if os.environ.get('TSLINK_SHUFFLE_SEEDS', ' '.join(SEEDS)).split() != list(SEEDS):
        raise ValueError('recorded seed policy differs')


def plan(family, repetitions, output):
    """Independent command inventory, also used by the mandatory collector."""
    items = []
    def add(i, label, kind, args, cwd=ROOT):
        items.append(dict(id=f'{family}-{i:02d}-{label}', family=family, iteration=i,
                          seed=SEEDS[(i-1) % 3], kind=kind, args=args, cwd=str(cwd)))
    for i in range(1, repetitions+1):
        seed = SEEDS[(i-1) % 3]
        go = ['go', 'test', '-json', '-count=1', '-timeout=9m', '-shuffle='+seed]
        if family == 'windows-root':
            add(i, 'root-race', 'go', go+['-race', './...'])
            add(i, 'cmd-server-load', 'go', go+['-race', './cmd', './internal/server'])
        elif family.endswith('-nested'):
            add(i, 'normal', 'go', go+['./...'], ROOT/'third_party/fsnotify')
            add(i, 'race', 'go', go+['-race', './...'], ROOT/'third_party/fsnotify')
        elif family == 'linux-root':
            profile = str(output/f'{family}-{i:02d}.coverage')
            add(i, 'race-coverage', 'go', go+['-race', '-coverprofile='+profile, './...'])
            add(i, 'coverage-threshold', 'coverage', ['go', 'tool', 'cover', '-func='+profile])
        elif family == 'macos-control':
            add(i, 'root-race', 'go', go+['-race', './...'])
        elif family == 'windows-smoke':
            shell = ['powershell.exe', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'RemoteSigned', '-File']
            add(i, 'reader', 'reader', shell+[str(ROOT/'scripts/windows-supervision-smoke-reader-test.ps1')])
            binary = str(output/f'{family}-{i:02d}.exe')
            add(i, 'build', 'build', ['go', 'build', '-trimpath', '-o', binary, '.'])
            add(i, 'lifecycle', 'smoke', shell+[str(ROOT/'scripts/windows-supervision-smoke.ps1'), '-Binary', binary,
                                              '-ReceiptPath', str(output/f'{family}-{i:02d}-cleanup.json')])
        else:
            raise ValueError(family)
    for item in items:
        item['contract'] = [arg.replace(str(output), '{output}').replace(str(ROOT), '{root}').replace('\\', '/') for arg in item['args']]
    return items


def go_results(text, required=()):
    events = [json.loads(line) for line in text.splitlines() if line.strip()]
    run, terminal, passed, skipped, failed = set(), set(), set(), set(), set()
    outputs = {}
    package_pass, observed, started, packages_with_tests = set(), set(), set(), set()
    package_terminal = {}
    errors = []
    for e in events:
        action, pkg, test = e.get('Action'), e.get('Package'), e.get('Test')
        if not pkg or not action:
            raise ValueError('invalid go test JSON event')
        key = (pkg, test)
        observed.add(pkg)
        if not test and action == 'start':
            if pkg in started: errors.append('duplicate package start: '+pkg)
            started.add(pkg)
        if pkg not in started: errors.append('event before package start: '+pkg)
        if pkg in package_terminal: errors.append('event after package terminal: '+pkg)
        if test:
            packages_with_tests.add(pkg)
            if action == 'output': outputs.setdefault(key, []).append(e.get('Output', ''))
            if action == 'run':
                if key in run: raise ValueError('repeated test in one count=1 receipt')
                run.add(key)
            if action in ('pass', 'skip', 'fail'):
                if key in terminal: errors.append('duplicate/inconsistent test terminal: '+pkg+':'+test)
                terminal.add(key)
                {'pass': passed, 'skip': skipped, 'fail': failed}[action].add(key)
        elif action in ('pass', 'skip', 'fail'):
            if pkg in package_terminal: errors.append('duplicate/inconsistent package terminal: '+pkg)
            package_terminal[pkg] = action
            if action == 'pass': package_pass.add(pkg)
            elif action == 'fail': failed.add(key)
    if observed != started or observed != set(package_terminal):
        errors.append('missing package start/terminal events: '+', '.join(sorted(
            (observed - started) | (observed - set(package_terminal)))))
    for pkg, action in package_terminal.items():
        if action == 'skip' and pkg in packages_with_tests:
            errors.append('package skip with test events: '+pkg)
    if not run or not passed or not package_pass: errors.append('zero executed/passing tests or packages')
    if run != terminal: errors.append('missing or unsolicited test terminal events')
    if failed: errors.append('failed tests/packages')
    passed_names = {test for _, test in passed}
    for name in required:
        if name not in passed_names: errors.append('required business test did not pass: '+name)
    # Exact existing source sites, not a blanket exemption for a whole test.
    # Required business tests above must pass even at an allowlisted skip site.
    policy = json.loads((ROOT/'.github/scripts/diagnostic-skips.json').read_text())
    for pkg, test in sorted(skipped):
        allowed = policy.get(pkg, [])
        reason = ''.join(outputs.get((pkg, test), []))
        if not any(re.search(r'^\s+'+re.escape(site)+r':', reason, re.M) for site in allowed):
            errors.append('unapproved skip: '+pkg+':'+test)
    return dict(canonical_run=sum('/' not in test for _, test in run), expanded_run=len(run),
                passed=len(passed), failed=len(failed), skipped=[dict(package=p, test=t, output=''.join(outputs.get((p,t), []))) for p,t in sorted(skipped)],
                packages=len(package_pass)), errors


def assess(item, receipt, output):
    errors = []
    if receipt.get('state') != 'DONE' or receipt.get('exit') != 0:
        errors.append(f"{receipt.get('state')} exit={receipt.get('exit')}")
    corrupt = False
    for suffix in ('stdout', 'stderr'):
        path = output/(item['id']+'.'+suffix)
        if not path.is_file() or receipt.get(suffix+'_sha256') != digest(path):
            errors.append('missing/corrupt '+suffix)
            corrupt = True
    if corrupt: return {}, errors
    text = (output/(item['id']+'.stdout')).read_text(encoding='utf-8')
    counts = {}
    if item['kind'] == 'go':
        if item['family'] == 'windows-nested':
            required = ['TestWindowsCloseJoinsPublicChannels','TestWindowsLateCompletionOwnership','TestWindowsSendErrorCloseToken']
        elif item['family'] == 'linux-nested':
            required = ['TestInotifyCloseJoinsPublicChannels','TestInotifyRepeatedCloseJoinsReader','TestInotifyCloseUnblocksUnreadChannels']
        else:
            required = ['TestGuestWriterLatency','TestGuestListenerRestoreFlushOwnership','TestGuestHealthyRequestFlushOwnership']
        counts, go_errors = go_results(text, required)
        errors.extend(go_errors)
    elif item['kind'] == 'coverage':
        match = re.search(r'^total:\s+\(statements\)\s+(\d+(?:\.\d+)?)%', text, re.M)
        if not match or float(match[1]) < 85.0: errors.append('missing coverage or below 85.0%')
    elif item['kind'] == 'reader':
        results = re.findall(r'^RESULT passed=(\d+) failed=(\d+)\s*$', text, re.M)
        checks = re.findall(r'^PASS (.+)\r?$', text, re.M)
        checks = [name.rstrip('\r') for name in checks]
        if results != [('28', '0')]: errors.append('missing complete native reader checks (28 required)')
        if len(checks) != 28 or set(checks) != set(READER_CHECKS):
            errors.append('native reader check inventory mismatch')
        sources = re.findall(r'^SOURCE ([0-9a-fA-F]{64})\s*$', text, re.M)
        if [source.lower() for source in sources] != [digest(ROOT/'scripts/windows-supervision-smoke.ps1')]:
            errors.append('native reader SOURCE differs from checked-out smoke script')
        counts['checks'] = len(checks)
    elif item['kind'] == 'smoke':
        path = output/f"{item['family']}-{item['iteration']:02d}-cleanup.json"
        cleanup = json.loads(path.read_text(encoding='utf-8-sig')) if path.is_file() else {}
        if cleanup.get('cleanup_proven') is not True or cleanup.get('passed') is not True:
            errors.append('lifecycle/cleanup not proved')
        if 'PASS Windows supervision: install, runtime artifact, crash restart, graceful stop, reinstall, uninstall' not in text:
            errors.append('missing complete lifecycle marker')
    return counts, errors


def execute(item, output, identity, blocked=None):
    receipt = {**item, **identity, 'state': 'NOTRUN', 'exit': None, 'reason': blocked,
               'started_ns': time.time_ns(), 'process_pid': None}
    if not blocked:
        receipt['state'] = 'DONE'
        try:
            # Fresh process, never retry. Go's inner timeout produces stacks
            # before this outer watchdog; a timeout remains a failed receipt.
            with (output/(item['id']+'.stdout')).open('wb') as out, (output/(item['id']+'.stderr')).open('wb') as err:
                child = subprocess.Popen(item['args'], cwd=item['cwd'], stdout=out, stderr=err, start_new_session=os.name != 'nt')
                receipt['process_pid'] = child.pid
                try: receipt['exit'] = child.wait(timeout=600)
                except subprocess.TimeoutExpired:
                    if os.name == 'nt':
                        subprocess.run(['taskkill', '/PID', str(child.pid), '/T', '/F'], stdout=err, stderr=err, timeout=30)
                    else: os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
                    receipt.update(state='TIMEOUT', exit=124)
        except (OSError, subprocess.SubprocessError) as exc:
            receipt.update(state='ERROR', reason=str(exc))
    receipt['finished_ns'] = time.time_ns()
    for suffix in ('stdout', 'stderr'):
        path = output/(item['id']+'.'+suffix)
        if path.is_file(): receipt[suffix+'_sha256'] = digest(path)
    try: receipt['counts'], receipt['errors'] = assess(item, receipt, output)
    except (ValueError, OSError) as exc: receipt['errors'] = [str(exc)]
    write(output/(item['id']+'.json'), receipt)
    return receipt


def run_family(output):
    validate()
    sha, reps = inputs()
    family = os.environ['DIAGNOSTIC_FAMILY']
    if family not in FAMILIES: raise ValueError('unknown family')
    if family == 'windows-smoke' and (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_OS') != 'Windows'):
        raise ValueError('real lifecycle smoke requires a disposable hosted Windows runner')
    output.mkdir(parents=True, exist_ok=False)
    identity = dict(sha=sha, run_id=os.environ['GITHUB_RUN_ID'], attempt=os.environ['GITHUB_RUN_ATTEMPT'], family=family)
    write(output/(family+'-metadata.json'), {**identity, 'repetitions': reps,
          'go_version': subprocess.check_output(['go', 'version'], text=True).strip(),
          'go_env': json.loads(subprocess.check_output(['go','env','-json','GOOS','GOARCH','GOVERSION'], text=True)),
          'runner_os': os.environ.get('RUNNER_OS'), 'image_os': os.environ.get('ImageOS'), 'image_version': os.environ.get('ImageVersion'),
          'workflow_sha': os.environ.get('GITHUB_WORKFLOW_SHA'), 'tree': subprocess.check_output(['git','rev-parse','HEAD^{tree}'], cwd=ROOT, text=True).strip()})
    results, unsafe_smoke, build_failed = [], False, False
    for item in plan(family, reps, output):
        blocked = None
        if item['kind'] == 'smoke':
            if unsafe_smoke: blocked = 'previous lifecycle cleanup not proved; unsafe reuse refused'
            elif build_failed: blocked = 'this iteration binary build failed'
        result = execute(item, output, identity, blocked)
        results.append(result)
        if item['kind'] == 'build': build_failed = bool(result['errors'])
        if item['kind'] == 'smoke' and not blocked:
            path = output/f'{family}-{item["iteration"]:02d}-cleanup.json'
            try: unsafe_smoke = json.loads(path.read_text(encoding='utf-8-sig')).get('cleanup_proven') is not True
            except (OSError, ValueError): unsafe_smoke = True
    failed = any(r['errors'] for r in results)
    write(output/(family+'-outcome.json'), {**identity, 'exit': int(failed), 'expected': len(plan(family,reps,output)), 'recorded': len(results)})
    return int(failed)


def collect(output):
    sha, reps = inputs()
    output.mkdir(parents=True, exist_ok=True)
    errors, summaries, starts, trees = [], [], set(), set()
    expected_tree = None
    try:
        validate()
        expected_tree = subprocess.check_output(['git', 'rev-parse', sha+'^{tree}'], cwd=ROOT, text=True).strip()
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        errors.append('collector source identity: '+str(exc))
    identity = dict(sha=sha, run_id=os.environ['GITHUB_RUN_ID'], attempt=os.environ['GITHUB_RUN_ATTEMPT'])
    for key in ('PREPARE_RESULT','FAMILIES_RESULT'):
        if os.environ.get(key) != 'success': errors.append(key+'='+str(os.environ.get(key)))
    expected_files = set()
    for family in FAMILIES:
        inventory = plan(family, reps, output)
        try:
            meta = json.loads((output/(family+'-metadata.json')).read_text())
            outcome = json.loads((output/(family+'-outcome.json')).read_text())
            if any(meta.get(k) != v or outcome.get(k) != v for k,v in identity.items()): raise ValueError('run identity skew')
            if meta.get('family') != family or outcome.get('family') != family: raise ValueError('family skew')
            if not meta.get('go_version') or not re.fullmatch('[0-9a-f]{40}', meta.get('tree','')) or meta.get('repetitions') != reps: raise ValueError('missing metadata')
            trees.add(meta['tree'])
            if expected_tree is not None and meta['tree'] != expected_tree: raise ValueError('family source tree differs from requested SHA tree')
            if outcome.get('expected') != len(inventory) or outcome.get('recorded') != len(inventory) or outcome.get('exit') != 0: raise ValueError('family outcome/count not successful')
        except (ValueError, OSError) as exc: errors.append(family+': '+str(exc))
        for item in inventory:
            expected_files.add(item['id']+'.json')
            try:
                receipt = json.loads((output/(item['id']+'.json')).read_text())
                if any(receipt.get(k) != v for k,v in identity.items()): raise ValueError('command identity skew')
                for key in ('id','family','iteration','seed','kind','contract'):
                    if receipt.get(key) != item[key]: raise ValueError('command inventory skew: '+key)
                if receipt.get('state') == 'DONE':
                    token = (family, receipt.get('process_pid'), receipt.get('started_ns'))
                    if token in starts or not token[1] or not token[2]: raise ValueError('missing/reused process receipt')
                    starts.add(token)
                counts, failures = assess(item, receipt, output)
                summaries.append(dict(id=item['id'], counts=counts, errors=failures))
                errors.extend(item['id']+': '+e for e in failures)
            except (ValueError, OSError) as exc: errors.append(item['id']+': '+str(exc))
    actual = {p.name for p in output.glob('*.json') if re.match(r'.*-\d{2}-(?!cleanup)', p.name)}
    if actual != expected_files: errors.append('receipt inventory mismatch')
    if len(trees) != 1: errors.append('different/missing source trees across families')
    write(output/'reconciliation.json', dict(**identity, expected_tree=expected_tree, expected=len(expected_files), observed=len(actual), errors=errors, commands=summaries))
    print(json.dumps(dict(expected=len(expected_files), observed=len(actual), errors=errors), indent=2))
    return int(bool(errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['validate','run','collect'])
    parser.add_argument('--output', type=Path, default=Path('diagnostic'))
    args = parser.parse_args()
    if args.mode == 'validate': validate(); return 0
    if args.mode == 'run': return run_family(args.output.resolve())
    return collect(args.output.resolve())


if __name__ == '__main__':
    sys.exit(main())
