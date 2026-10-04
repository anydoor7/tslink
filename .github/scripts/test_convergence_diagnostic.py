"""Hermetic executable failure-propagation controls; never run lifecycle smoke."""
import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('diagnostic', Path(__file__).with_name('convergence-diagnostic.py'))
d = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(d)


def events(names=('TestControl',), action='pass'):
    tests = [x for name in names for x in (
        {'Action':'run','Package':'fixture','Test':name},
        {'Action':action,'Package':'fixture','Test':name})]
    return '\n'.join(json.dumps(x) for x in [
        {'Action':'start','Package':'fixture'}, *tests,
        {'Action':'pass','Package':'fixture'}])+'\n'


def source_identity():
    return subprocess.check_output(['git', 'rev-parse', 'HEAD', 'HEAD^{tree}'], cwd=d.ROOT, text=True).splitlines()


def reader_output():
    # Independent expected inventory: do not import the collector's allowlist.
    names = [
        'unlocked PID control', 'old RED and shared writer GREEN',
        'replacement returns fresh contents', 'delete-access handle is shared',
        'missing PID never becomes fresh', 'reject PID [111]', 'reject PID [0]',
        'reject PID [-2]', 'reject PID [not-a-pid]', 'reject PID [222 extra]', 'reject PID []',
        'single-attempt shared read still reports exclusive lock',
        'bounded PID observer treats sharing conflict as pending', 'permanent invalid path remains an error',
        'verified daemon and matching artifact control', 'runtime reader coexists with writer',
        'persistent runtime lock fails at the original deadline',
        'supervisor accounting accepts one crash and rejects absorbed failure',
        'reject not running', 'reject old PID', 'reject unowned daemon', 'reject no restart policy',
        'reject wrong autostart scope', 'reject wrong runtime PID', 'reject stale artifact',
        'reject future artifact', 'reject missing artifact', 'malformed artifact is an error',
    ]
    return 'SOURCE '+d.digest(d.ROOT/'scripts/windows-supervision-smoke.ps1').upper()+'\n'+''.join(
        'PASS '+name+'\n' for name in names)+'RESULT passed=28 failed=0\n'


class DiagnosticTests(unittest.TestCase):
    def setUp(self):
        sha, _ = source_identity()
        self.env = mock.patch.dict(os.environ, {'DIAGNOSTIC_SHA':sha,'GITHUB_SHA':sha,'DIAGNOSTIC_REPETITIONS':'1',
            'GITHUB_RUN_ID':'123','GITHUB_RUN_ATTEMPT':'1','PREPARE_RESULT':'success','FAMILIES_RESULT':'success'})
        self.env.start()
        self.addCleanup(self.env.stop)

    def fixture(self, out):
        sha, tree = source_identity()
        identity = dict(sha=sha,run_id='123',attempt='1')
        for family in d.FAMILIES:
            plan = d.plan(family,1,out)
            d.write(out/(family+'-metadata.json'),dict(**identity,family=family,repetitions=1,go_version='go1.fixture',tree=tree))
            d.write(out/(family+'-outcome.json'),dict(**identity,family=family,exit=0,expected=len(plan),recorded=len(plan)))
            for index,item in enumerate(plan):
                if family == 'windows-nested': names=['TestWindowsCloseJoinsPublicChannels','TestWindowsLateCompletionOwnership','TestWindowsSendErrorCloseToken']
                elif family == 'linux-nested': names=['TestInotifyCloseJoinsPublicChannels','TestInotifyRepeatedCloseJoinsReader','TestInotifyCloseUnblocksUnreadChannels']
                else: names=['TestGuestWriterLatency','TestGuestListenerRestoreFlushOwnership','TestGuestHealthyRequestFlushOwnership']
                text = events(names)
                if item['kind'] == 'coverage': text='total: (statements) 85.1%\n'
                if item['kind'] == 'reader': text=reader_output()
                if item['kind'] == 'smoke':
                    text='PASS Windows supervision: install, runtime artifact, crash restart, graceful stop, reinstall, uninstall\n'
                    d.write(out/f'{family}-01-cleanup.json',dict(cleanup_proven=True,passed=True))
                (out/(item['id']+'.stdout')).write_text(text)
                (out/(item['id']+'.stderr')).write_text('')
                receipt=dict(**item,**identity,state='DONE',exit=0,process_pid=index+100,started_ns=index+1000)
                for suffix in ('stdout','stderr'): receipt[suffix+'_sha256']=d.digest(out/(item['id']+'.'+suffix))
                d.write(out/(item['id']+'.json'),receipt)

    def verdict(self, out):
        with contextlib.redirect_stdout(io.StringIO()): return d.collect(out)

    def test_complete_control_and_independent_negative_inventory(self):
        mutations = ['missing','wrong-sha','exit','zero','skip','corrupt','notrun','family-failure','job-cancelled','contract','cleanup','required-test']
        for mutation in mutations:
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                out=Path(tmp); self.fixture(out)
                self.assertEqual(self.verdict(out),0,'positive control')
                path=out/'windows-root-01-root-race.json'
                receipt=json.loads(path.read_text())
                if mutation=='missing': path.unlink()
                elif mutation=='wrong-sha': receipt['sha']='b'*40
                elif mutation=='exit': receipt['exit']=1
                elif mutation=='notrun': receipt['state']='NOTRUN'
                elif mutation=='contract': receipt['contract']=['go','test','./wrong']
                elif mutation=='family-failure':
                    p=out/'linux-nested-outcome.json'; obj=json.loads(p.read_text());obj['exit']=1;d.write(p,obj)
                elif mutation=='job-cancelled': os.environ['FAMILIES_RESULT']='cancelled'
                elif mutation=='cleanup': d.write(out/'windows-smoke-01-cleanup.json',dict(cleanup_proven=False,passed=True))
                else:
                    log=out/'windows-root-01-root-race.stdout'
                    log.write_text('' if mutation=='zero' else events(action='skip') if mutation=='skip' else events())
                    if mutation!='corrupt': receipt['stdout_sha256']=d.digest(log)
                if mutation!='missing': d.write(path,receipt)
                self.assertEqual(self.verdict(out),1,mutation)
                os.environ['FAMILIES_RESULT']='success'

    def test_records_failure_then_executes_independent_command(self):
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp)
            receipts=[]
            for n,code in enumerate([7,0]):
                item=dict(id=f'control-{n}',family='fixture',iteration=1,kind='build',seed=d.SEEDS[0],cwd=str(out),
                          args=[sys.executable,'-c',f'print("executed-{n}");raise SystemExit({code})'])
                receipts.append(d.execute(item,out,dict(sha='a'*40)))
            self.assertTrue(receipts[0]['errors'])
            self.assertFalse(receipts[1]['errors'])
            self.assertEqual((out/'control-1.stdout').read_text().strip(),'executed-1')

    def test_nonzero_go_exit_keeps_counts_and_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp); self.fixture(out)
            item = d.plan('windows-root', 1, out)[0]
            path = out/(item['id']+'.json')
            receipt = json.loads(path.read_text())
            receipt['exit'] = 1
            counts, errors = d.assess(item, receipt, out)
            self.assertEqual(counts['passed'], 3)
            self.assertEqual(errors, ['DONE exit=1'])
            log = out/(item['id']+'.stdout')
            text = log.read_text().replace('"Action": "pass"', '"Action": "fail"')
            log.write_text(text)
            receipt['stdout_sha256'] = d.digest(log)
            counts, errors = d.assess(item, receipt, out)
            self.assertEqual(counts['failed'], 4)  # three tests plus their package
            self.assertIn('DONE exit=1', errors)
            self.assertIn('failed tests/packages', errors)
            d.write(path, receipt)
            self.assertEqual(self.verdict(out), 1)

    def test_preexisting_helper_skips_are_exact_caller_sites(self):
        sites = [
            ('internal/config', 'TestConfigMessagesNameABackslashPath', 'windows_path_rendering_test.go:47'),
            ('internal/credentials', 'TestDeleteCredentialFilePathStrictReportsAnUnreadableReadback', 'credentials_strict_delete_test.go:162'),
            ('internal/testenv', 'TestPlantedShimRefusesAndRecords', 'service_manager_path_shim_test.go:42'),
            ('internal/testenv', 'TestPlantedShimRecordsOneLinePerConcurrentCall', 'service_manager_path_shim_test.go:88'),
            ('internal/testenv', 'TestReadServiceManagerShimCallsDistinguishesEmptyFromAbsent', 'service_manager_path_shim_test.go:167'),
            ('cmd', 'TestSystemdInstallE2E', 'install_linux_e2e_test.go:54'),
        ]
        for pkg, name, site in sites:
            with self.subTest(site=site):
                pkg = 'github.com/anydoor7/tslink/'+pkg
                values = [{'Action':'start','Package':pkg},
                          {'Action':'run','Package':pkg,'Test':name},
                          {'Action':'output','Package':pkg,'Test':name,'Output':'    '+site+': existing platform/opt-in skip\n'},
                          {'Action':'skip','Package':pkg,'Test':name},
                          {'Action':'pass','Package':pkg}]
                encode = lambda: events()+'\n'.join(json.dumps(e) for e in values)+'\n'
                counts, errors = d.go_results(encode())
                self.assertFalse(errors)
                self.assertEqual(len(counts['skipped']), 1)
                self.assertIn('required business test did not pass: '+name, d.go_results(encode(), [name])[1])
                values[2]['Output'] = '    '+site+'0: unregistered neighboring line\n'
                self.assertIn('unapproved skip: '+pkg+':'+name, d.go_results(encode())[1])

    def test_go_package_start_terminal_completeness(self):
        control = events()
        extra = [{'Action':'start','Package':'other'},
                 {'Action':'output','Package':'other','Output':'? other [no test files]\n'},
                 {'Action':'skip','Package':'other'}]
        encode = lambda values: '\n'.join(json.dumps(e) for e in values)+'\n'
        self.assertFalse(d.go_results(control+encode(extra))[1], 'explicit no-test skip control')
        complete = [json.loads(line) for line in control.splitlines()]
        mutations = {
            'missing package start': complete[1:],
            'missing package terminal': complete[:-1],
            'duplicate package start': [complete[0], *complete],
            'duplicate package terminal': [*complete, complete[-1]],
            'inconsistent package terminal': [*complete, {'Action':'skip','Package':'fixture'}],
            'package skip with test events': [*complete[:-1], {'Action':'skip','Package':'fixture'}],
            'duplicate/inconsistent test terminal': [*complete[:-1], complete[-2], complete[-1]],
        }
        for name, values in mutations.items():
            with self.subTest(name=name):
                errors = d.go_results(encode(values))[1]
                reason = 'missing package start/terminal' if name.startswith('missing package') else (
                    'duplicate/inconsistent package terminal' if name.endswith('package terminal') else name)
                self.assertTrue(any(reason in error for error in errors), errors)
        self.assertTrue(any('other' in error and 'missing package' in error
                            for error in d.go_results(control+encode(extra[:-1]))[1]))

    def test_collector_binds_real_checkout_dispatch_and_requested_tree(self):
        sha, tree = source_identity()
        self.assertEqual(d.ROOT, Path(__file__).resolve().parents[2])
        for mutation in ('tree', 'dispatch', 'checkout'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                out = Path(tmp); self.fixture(out)
                self.assertEqual(self.verdict(out), 0)
                self.assertEqual(json.loads((out/'reconciliation.json').read_text())['expected_tree'], tree)
                if mutation == 'tree':
                    # A real different tree, also available in shallow CI checkouts.
                    other_tree = subprocess.check_output(['git','rev-parse','HEAD:.github'],cwd=d.ROOT,text=True).strip()
                    self.assertNotEqual(other_tree, tree)
                    for path in out.glob('*-metadata.json'):
                        value=json.loads(path.read_text()); value['tree']=other_tree; d.write(path,value)
                    env = {}
                else:
                    self.assertNotEqual(sha, tree)
                    env = {'GITHUB_SHA':tree}
                    if mutation == 'checkout': env['DIAGNOSTIC_SHA']=tree
                with mock.patch.dict(os.environ,env): self.assertEqual(self.verdict(out),1)
                errors = json.loads((out/'reconciliation.json').read_text())['errors']
                reason = 'family source tree differs' if mutation == 'tree' else 'checkout, dispatch workflow and requested SHA'
                self.assertTrue(any(reason in e for e in errors), errors)

    def test_reader_requires_current_source_and_each_named_check(self):
        mutations = ('old-count', 'source', 'missing-source', 'missing-check', 'duplicate-check', 'summary-only')
        for mutation in mutations:
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                out=Path(tmp); self.fixture(out)
                self.assertEqual(self.verdict(out),0)
                log=out/'windows-smoke-01-reader.stdout'; text=log.read_text()
                boundary='PASS supervisor accounting accepts one crash and rejects absorbed failure\n'
                if mutation=='old-count': text=text.replace('passed=28','passed=24')
                elif mutation=='source': text=text.replace(text.splitlines()[0], 'SOURCE '+'0'*64)
                elif mutation=='missing-source': text='\n'.join(text.splitlines()[1:])+'\n'
                elif mutation=='missing-check': text=text.replace(boundary,'PASS unrelated check\n')
                elif mutation=='duplicate-check': text=text.replace(boundary,'PASS unlocked PID control\n')
                else: text='RESULT passed=28 failed=0\n'
                log.write_text(text)
                path=out/'windows-smoke-01-reader.json'; receipt=json.loads(path.read_text())
                receipt['stdout_sha256']=d.digest(log); d.write(path,receipt)
                self.assertEqual(self.verdict(out),1)
                errors=json.loads((out/'reconciliation.json').read_text())['errors']
                self.assertTrue(all(error.startswith('windows-smoke-01-reader:') for error in errors), errors)

    def test_bounded_fresh_process_inventory(self):
        for reps in [1,10]:
            items=[x for f in d.FAMILIES for x in d.plan(f,reps,Path('/fixture'))]
            self.assertEqual(len(items),12*reps)
            self.assertEqual(len({x['id'] for x in items}),len(items))
            for x in items:
                self.assertEqual(x['seed'],d.SEEDS[(x['iteration']-1)%3])
                if x['kind']=='go': self.assertIn('-count=1',x['args'])
        for bad in ['0','11','1.0','01','-1']:
            with mock.patch.dict(os.environ,{'DIAGNOSTIC_REPETITIONS':bad}):
                with self.assertRaises(ValueError): d.inputs()

    def test_workflow_collects_on_failure_and_bounds_parallelism(self):
        text=(d.ROOT/'.github/workflows/convergence-diagnostic.yml').read_text()
        self.assertIn('max-parallel: 4',text)
        self.assertIn('fail-fast: false',text)
        self.assertIn('timeout-minutes: 45',text)
        self.assertIn('needs: [prepare, families]',text)
        self.assertNotIn('continue-on-error',text)
        self.assertIn('if: always()',text)
        for f in d.FAMILIES: self.assertIn('family: '+f,text)

    def test_failed_cleanup_stops_only_lifecycle_reuse(self):
        for cleaned in [False,True]:
            with self.subTest(cleaned=cleaned), tempfile.TemporaryDirectory() as tmp:
                out=Path(tmp)/'receipts'; calls=[]
                def fake_execute(item,output,identity,blocked=None):
                    calls.append((item['kind'],item['iteration'],blocked))
                    if item['kind']=='smoke' and not blocked:
                        d.write(output/f'windows-smoke-{item["iteration"]:02d}-cleanup.json',dict(cleanup_proven=cleaned))
                    return dict(errors=['intentional failure'] if item['kind']=='smoke' else [])
                with mock.patch.dict(os.environ,{'DIAGNOSTIC_FAMILY':'windows-smoke','DIAGNOSTIC_REPETITIONS':'2','GITHUB_ACTIONS':'true','RUNNER_OS':'Windows'}), \
                     mock.patch.object(d,'validate'), mock.patch.object(d,'execute',side_effect=fake_execute), \
                     mock.patch.object(d.subprocess,'check_output',side_effect=['go1.fixture','{}','b'*40]):
                    self.assertEqual(d.run_family(out),1)
                self.assertEqual(len(calls),6)
                self.assertIsNone(calls[3][2]) # later independent reader executes
                self.assertIsNone(calls[4][2]) # later independent build executes
                self.assertEqual(calls[5][2] is None,cleaned)


if __name__=='__main__': unittest.main()
