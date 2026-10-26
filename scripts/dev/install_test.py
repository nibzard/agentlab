#!/usr/bin/env python3
"""Run the real installer against isolated fake release downloads."""
import hashlib, io, json, os, pathlib, shutil, subprocess, sys, tarfile, tempfile, unittest
SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'install.sh'

class InstallerTest(unittest.TestCase):
 def install(self, downloader='curl', failure=''):
  with tempfile.TemporaryDirectory() as root:
   root=pathlib.Path(root); tools=root/'tools';tools.mkdir();tmp=root/'tmp';tmp.mkdir();prefix=root/'prefix';(prefix/'bin').mkdir(parents=True)
   old=prefix/'bin'/'agentlab';old.write_text('old binary')
   for name in ['bash','uname','tr','grep','head','sed','cat','awk','mktemp','rm','tar','find','mkdir','install','sha256sum','shasum']:
    path=shutil.which(name)
    if path:(tools/name).symlink_to(path)
   if failure=='install':
    (tools/'install').unlink()
    (tools/'install').write_text('#!/bin/sh\nexit 13\n');(tools/'install').chmod(0o755)
   archive=root/'fixture.tar.gz'
   with tarfile.open(archive,'w:gz') as tar:
    payload=b'#!/bin/sh\necho fixture-v1\n';info=tarfile.TarInfo('agentlab');info.size=len(payload);info.mode=0o755;tar.addfile(info,io.BytesIO(payload))
   if failure=='extraction':archive.write_bytes(b'not a tarball')
   platform=subprocess.check_output([shutil.which('bash'),'-c','uname -s | tr "[:upper:]" "[:lower:]"'],text=True).strip()
   arch='arm64' if os.uname().machine in ('arm64','aarch64') else 'amd64'
   name=f'agentlab_v1.0.0_{platform}-{arch}.tar.gz'
   digest=hashlib.sha256(archive.read_bytes()).hexdigest() if failure!='checksum' else '0'*64
   (root/'checksums.txt').write_text(f'{digest}  {name}\n')
   fake=tools/downloader
   fake.write_text(f'#!{sys.executable}\n'+'''import sys,os,pathlib,shutil,json
args=sys.argv[1:];out=args[args.index('-o' if '-o' in args else '-O')+1];url=next(a for a in args if a.startswith('https://'))
root=pathlib.Path(os.environ['FIXTURE_ROOT'])
if os.environ.get('FAILURE')=='download':sys.exit(7)
if url.endswith('/latest'):pathlib.Path(out).write_text(json.dumps({'tag_name':'v1.0.0'}))
elif url.endswith('checksums.txt'):shutil.copy(root/'checksums.txt',out)
else:shutil.copy(root/'fixture.tar.gz',out)
''');fake.chmod(0o755)
   env={**os.environ,'PATH':str(tools),'TMPDIR':str(tmp),'AGENTLAB_PREFIX':str(prefix),'AGENTLAB_VERSION':'latest','FIXTURE_ROOT':str(root),'FAILURE':failure}
   result=subprocess.run([shutil.which('bash'),str(SCRIPT)],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
   if failure:
    self.assertNotEqual(result.returncode,0,result.stdout+result.stderr);self.assertEqual(old.read_text(),'old binary')
   else:self.assertEqual(result.returncode,0,result.stdout+result.stderr);self.assertIn('fixture-v1',old.read_text())
   self.assertEqual(list(tmp.iterdir()),[],f'leaked temporary files: {result.stdout} {result.stderr}')
 def test_curl(self):self.install('curl')
 def test_wget(self):self.install('wget')
 def test_checksum_failure(self):self.install(failure='checksum')
 def test_download_failure(self):self.install(failure='download')
 def test_extraction_failure(self):self.install(failure='extraction')
 def test_install_failure(self):self.install(failure='install')

if __name__=='__main__':unittest.main()
