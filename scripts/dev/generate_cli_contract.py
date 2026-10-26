#!/usr/bin/env python3
"""Generate CLI discovery from registered FlagSets; run from repository root."""
import json,re,os,argparse
from pathlib import Path
os.chdir(Path(__file__).resolve().parents[2])
commands={}
blocks=[]
for path in sorted(Path("cmd/agentlab").glob("*.go")):
 if not path.name.endswith("_test.go"):blocks.extend(re.split(r'(?m)^func ',path.read_text())[1:])
helpers={}
for block in blocks:
 match=re.match(r'(bind\w+)\(',block)
 if match:helpers[match[1]]=block
for path in sorted(Path("cmd/agentlab").glob("*.go")):
 if path.name.endswith("_test.go"):continue
 for block in re.split(r'(?m)^func ',path.read_text())[1:]:
  match=re.search(r'(?:newFlagSet|flag.NewFlagSet)\("([^"]+)"\s*[,)]',block)
  if not match or match[1]=="agentlab":continue
  name=match[1]
  for helper,body in helpers.items():
   if helper+"(fs)" in block:block+="\n"+body
  flags={}
  details={}
  for m in re.finditer(r'fs\.(StringVar|BoolVar|IntVar|DurationVar|Var)\(&([\w.]+),\s*"([^"]+)"',block):
   method,var,key=m.groups()
   kind={"StringVar":"string","BoolVar":"bool","IntVar":"int","DurationVar":"duration"}.get(method)
   if kind is None:
    kind="bool" if re.search(r'\b'+re.escape(var)+r'\s+optionalBool',block) else "string"
   flags[key]=kind
  for m in re.finditer(r'fs\.(?:StringVar|BoolVar|IntVar|DurationVar)\(&[\w.]+,\s*"([^"]+)",\s*([^,\n]+),\s*"((?:[^"\\]|\\.)*)"',block):
   key,default,description=m.groups()
   try: description=json.loads('"'+description+'"')
   except ValueError: pass
   if default.startswith('"'):
    try:default=json.loads(default)
    except ValueError:pass
   elif default not in ('true','false') and not default.isdigit():default="inherited or computed"
   details[key]={"description":description,"default":default}
  for m in re.finditer(r'fs.Var\(&[\w.]+,\s*"([^"]+)",\s*"((?:[^"\\]|\\.)*)"',block):
   details[m[1]]={"description":m[2],"default":"unset"}
  if "bindHelpFlag(fs)" in block: flags.update(help="bool",h="bool")
  # Infer unconditional required empty-string guards from actual handlers.
  # Conditional/default-dependent requirements stay in shared semantic rules.
  empty_flags={}
  for binding in re.finditer(r'fs.StringVar\(&([\w.]+),\s*"([^"]+)",\s*""',block):
   empty_flags.setdefault(binding[1],binding[2])
  required=[]
  for guard in re.finditer(r'(?m)^\tif ([^\n{]+) \{([^}]+)\}',block):
   variables=re.fullmatch(r'([\w.]+ == "")(?: \|\| [\w.]+ == "")*',guard[1])
   if variables and 'return newUsageError' in guard[2]:
    required.extend(empty_flags[var] for var in re.findall(r'([\w.]+) == ""',guard[1]) if var in empty_flags)
  minimum=re.search(r'fs.NArg\(\) < (\d+)',block)
  indexes=[int(index) for index in re.findall(r'fs.Arg\((\d+)\)',block)]
  limit=max([int(minimum[1]) if minimum else 0]+[index+1 for index in indexes])
  if name=="ssh":limit=1
  if name in ("sandbox new","sandbox validate","msg post"):limit=-1
  operand_types={index:"vmid" for index in re.findall(r'parseVMID\(fs.Arg\((\d+)\)\)',block)}
  # Local and remote secrets implementations contribute a union.
  previous=commands.get(name,{"flags":{}})
  flags={**previous["flags"],**flags}
  commands[name]={"flags":flags,"flag_details":{**previous.get("flag_details",{}),**details},"operands":max(previous.get("operands",limit),limit),"operand_types":{**previous.get("operand_types",{}),**operand_types},"required_flags":sorted(set(previous.get("required_flags",[])+required))}
commands.update({
 "operation show":{"flags":{},"operands":1},
 "operation wait":{"flags":{},"operands":1},
 "job wait":{"flags":{},"operands":1},
 "sandbox wait":{"flags":{},"operands":1,"operand_types":{"0":"vmid"}},
 "defaults write":{"flags":{},"operands":2},
 "defaults read":{"flags":{},"operands":1},
 "defaults delete":{"flags":{},"operands":1},
 "defaults list":{"flags":{},"operands":0},
 "defaults effective":{"flags":{},"operands":0},
 "completion":{"flags":{},"operands":1},
 "version":{"flags":{"json":"bool"},"operands":0},
})
for command in commands.values():command["flags"].update(help="bool",h="bool")
parser=argparse.ArgumentParser()
parser.add_argument("--check",action="store_true")
args=parser.parse_args()
path=Path("internal/clicontract/commands.json")
expected=json.dumps(commands,indent=2,sort_keys=True)+"\n"
if args.check:
 if not path.exists() or path.read_text()!=expected: raise SystemExit("CLI command catalog is out of date; run make docs-gen")
else:path.write_text(expected)
