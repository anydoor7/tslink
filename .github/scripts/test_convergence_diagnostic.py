"""Hermetic executable failure-propagation controls; never run lifecycle smoke."""
import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('diagnostic', Path(__file__).with_name('convergence-diagnostic.py'))
d = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(d)


def events(names=('TestControl',), action='pass'):
    return '\n'.join(json.dumps(x) for name in names for x in (
        {'Action':'run','Package':'fixture','Test':name},
        {'Action':action,'Package':'fixture','Test':name},
        {'Action':'pass','Package':'fixture'}))+'\n'


class DiagnosticTests(unittest.TestCase):
    def setUp(self):
        self.env = mock.patch.dict(os.environ, {'DIAGNOSTIC_SHA':'a'*40,'DIAGNOSTIC_REPETITIONS':'1',
            'GITHUB_RUN_ID':'123','GITHUB_RUN_ATTEMPT':'1','PREPARE_RESULT':'success','FAMILIES_RESULT':'success'})
        self.env.start()
        self.addCleanup(self.env.stop)

    def fixture(self, out):
        identity = dict(sha='a'*40,run_id='123',attempt='1')
        for family in d.FAMILIES:
            plan = d.plan(family,1,out)
            d.write(out/(family+'-metadata.json'),dict(**identity,family=family,repetitions=1,go_version='go1.fixture',tree='b'*40))
            d.write(out/(family+'-outcome.json'),dict(**identity,family=family,exit=0,expected=len(plan),recorded=len(plan)))
            for index,item in enumerate(plan):
                if family == 'windows-nested': names=['TestWindowsCloseJoinsPublicChannels','TestWindowsLateCompletionOwnership','TestWindowsSendErrorCloseToken']
                elif family == 'linux-nested': names=['TestInotifyCloseJoinsPublicChannels','TestInotifyRepeatedCloseJoinsReader','TestInotifyCloseUnblocksUnreadChannels']
                else: names=['TestGuestWriterLatency','TestGuestListenerRestoreFlushOwnership','TestGuestHealthyRequestFlushOwnership']
                text = events(names)
                if item['kind'] == 'coverage': text='total: (statements) 85.1%\n'
                if item['kind'] == 'reader': text='RESULT passed=28 failed=0\n'
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
