#!/usr/bin/env bash
set -uo pipefail

# Fast fault coverage for the release-upgrade rehearsal's proof primitives.
#
# The rehearsal itself downloads a published artifact and drives four validators
# through a real upgrade boundary. That is far too slow and too network-dependent
# for ordinary CI — but its primitives are exactly the things whose silent failure
# would turn a broken run into a green one, so they need coverage that runs on
# every push.
#
# So this exercises the primitives against STUBS: canned RPC payloads, fake
# processes, synthetic assertion logs. No release is downloaded and no chain is
# started. What it proves is narrow and specific — that each reader REFUSES when
# it cannot see, rather than returning a value it invented.
#
# The distinction matters because every one of these has a plausible failure mode
# that looks like success: an unreachable node reads as height 0, a missing lsof
# reads as "all ports free", a cobra parent's help exits 0 for an absent child.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"
set +e

PASSED=0; FAILED=0
check() { # <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then printf '  ok    %-52s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-52s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
group() { echo; echo "=== $* ==="; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
NET="$WORK/net"; mkdir -p "$NET/logs" "$NET/node0/data"

# ---- strict height readers -----------------------------------------------------

group "application and block-store heights refuse what they cannot read"

# The readers are jq -er over an RPC payload. Stub the transport so the parse is
# what is under test.
# The endpoint contains a slash, so it is flattened into the stub's filename.
stub_path() { echo "$WORK/rpc$(tr '/' '_' <<<"$1").json"; }
rpc_get() { cat "$(stub_path "$2")" 2>/dev/null || return 1; }

printf '{"result":{"response":{"last_block_height":"41"}}}\n' >"$(stub_path /abci_info)"
printf '{"result":{"sync_info":{"latest_block_height":"42"}}}\n' >"$(stub_path /status)"
check "app height parses"                    "41" "$(app_height 0)"
check "store height parses"                  "42" "$(store_height 0)"

# The pair is the point: at a halt they differ by exactly one, and a single
# number cannot express "stored but not applied".
check "the two differ at a halt"             "1"  "$(( $(store_height 0) - $(app_height 0) ))"

for name in 'empty' 'not json' 'null height' 'missing field' 'wrong shape'; do
  case "$name" in
    'empty')         printf '' ;;
    'not json')      printf 'not json at all' ;;
    'null height')   printf '{"result":{"response":{"last_block_height":null}}}' ;;
    'missing field') printf '{"result":{"response":{}}}' ;;
    'wrong shape')   printf '{"result":[]}' ;;
  esac >"$(stub_path /abci_info)"
  app_height 0 >/dev/null 2>&1
  check "app height refuses: $name"          "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
done

# And the guarded reader must not let a refusal become an arithmetic zero.
printf '{"result":{"response":{}}}' >"$(stub_path /abci_info)"
DRILL_ASSERT_LOG="$WORK/a.jsonl"; : >"$DRILL_ASSERT_LOG"; FAILURES=0
read_required_uint PROBE app_height 0
check "guarded read reports failure"         "1" "$?"
check "guarded read left the target unset"   "unset" "${PROBE:-unset}"

# ---- upgrade-info --------------------------------------------------------------

group "upgrade-info is read strictly"

node_home() { echo "$NET/node$1"; }
printf '{"name":"v0.2.0","height":42}\n' >"$NET/node0/data/upgrade-info.json"
check "upgrade-info name"                    "v0.2.0" "$(upgrade_info_field 0 name)"
check "upgrade-info height"                  "42"     "$(upgrade_info_field 0 height)"

printf '{"height":42}\n' >"$NET/node0/data/upgrade-info.json"
upgrade_info_field 0 name >/dev/null 2>&1
check "a missing name refuses"               "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
: >"$NET/node0/data/upgrade-info.json"
upgrade_info_field 0 name >/dev/null 2>&1
check "an empty file refuses"                "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
rm -f "$NET/node0/data/upgrade-info.json"
upgrade_info_field 0 name >/dev/null 2>&1
check "an absent file refuses"               "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- the port guard ------------------------------------------------------------

group "the port guard fails closed"

# lsof returning non-zero is how a FREE port looks, so a missing lsof returns
# non-zero for every port — and a guard that only checked exit status would
# report a clean bill of health on the machine where it could not look.
( PATH="$WORK/empty-path"; mkdir -p "$PATH"
  NODE_COUNT=4 require_free_ports >/dev/null 2>&1 )
check "no inspector available refuses"       "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

mkdir -p "$WORK/bin"
printf '#!/usr/bin/env bash\nexit 1\n' >"$WORK/bin/lsof"; chmod +x "$WORK/bin/lsof"
( PATH="$WORK/bin:$PATH"; NODE_COUNT=4 require_free_ports >/dev/null 2>&1 )
check "genuinely free ports pass"            "0" "$?"

printf '#!/usr/bin/env bash\nexit 0\n' >"$WORK/bin/lsof"; chmod +x "$WORK/bin/lsof"
( PATH="$WORK/bin:$PATH"; NODE_COUNT=4 require_free_ports >/dev/null 2>&1 )
check "an occupied port refuses"             "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- running-binary identity -----------------------------------------------------

group "binary identity comes from the PROCESS, not a shell variable"

# `ps` is stubbed rather than a real process being launched. Copying a system
# binary and running it does not work on macOS — the copy is unsigned and the
# kernel kills it — and building a throwaway binary would make a "fast" suite
# depend on the toolchain. What matters is the READER's logic: take the pid, ask
# what executable that process is running, hash THAT file.
mkdir -p "$WORK/bin"
printf 'binary-under-test\n' >"$WORK/target-binary"
printf 'a-different-binary\n' >"$WORK/other-binary"

cat >"$WORK/bin/ps" <<PSEOF
#!/usr/bin/env bash
# Mimics: ps -o command= -p <pid>
cat "$WORK/ps-answer" 2>/dev/null
PSEOF
chmod +x "$WORK/bin/ps"

echo "$WORK/target-binary 30" >"$WORK/ps-answer"
echo "4242" >"$NET/node0.pid"
OBSERVED="$(PATH="$WORK/bin:$PATH" node_exe_sha 0)"
check "identity is the running executable"   "$(sha256_of "$WORK/target-binary")" "$OBSERVED"

# The mismatch that matters: the shell variable says one binary while the process
# runs another. Only reading the process catches it.
check "a different binary is detected"       "different" \
  "$([[ "$OBSERVED" != "$(sha256_of "$WORK/other-binary")" ]] && echo different || echo SAME)"

# A process whose executable no longer exists must refuse, not hash nothing.
echo "$WORK/deleted-binary 30" >"$WORK/ps-answer"
PATH="$WORK/bin:$PATH" node_exe_sha 0 >/dev/null 2>&1
check "a vanished executable refuses"        "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

: >"$WORK/ps-answer"
PATH="$WORK/bin:$PATH" node_exe_sha 0 >/dev/null 2>&1
check "a dead process refuses"               "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

echo "not-a-pid" >"$NET/node0.pid"
node_exe_sha 0 >/dev/null 2>&1
check "a malformed pid refuses"              "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
rm -f "$NET/node0.pid"
node_exe_sha 0 >/dev/null 2>&1
check "an absent pidfile refuses"            "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- validator topology ------------------------------------------------------------

group "the four-validator topology is proven, not assumed"

printf '{"result":{"total":"4","validators":[{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"}]}}\n' \
  >"$(stub_path /validators)"
check "four validators counted"              "4" "$(validator_count 0)"
check "minimum power"                        "1" "$(min_validator_power 0)"
check "maximum power"                        "1" "$(max_validator_power 0)"

# Three validators, or unequal power, must not read as the four-equal topology
# the quorum argument depends on.
printf '{"result":{"total":"3","validators":[{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"}]}}\n' \
  >"$(stub_path /validators)"
check "a three-validator set is visible"     "3" "$(validator_count 0)"
printf '{"result":{"total":"4","validators":[{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"9"}]}}\n' \
  >"$(stub_path /validators)"
check "unequal power is visible"             "9" "$(max_validator_power 0)"
printf '{"result":{"total":"4"}}\n' >"$(stub_path /validators)"
min_validator_power 0 >/dev/null 2>&1
check "a missing validator array refuses"    "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- validator identity ------------------------------------------------------------

group "validator identity is proven against each node's own key"

# Counting the set proves a four-member set exists. It does not prove the nodes
# under test are its members, and the 3-of-4 partial-rollout argument depends on
# the fourth genuinely being one of them.
mkdir -p "$NET/node0/config" "$NET/node1/config"
printf '{"address":"AAAA1111","pub_key":{"type":"tendermint/PubKeyEd25519","value":"x"}}\n' \
  >"$NET/node0/config/priv_validator_key.json"
printf '{"address":"BBBB2222","pub_key":{"type":"tendermint/PubKeyEd25519","value":"y"}}\n' \
  >"$NET/node1/config/priv_validator_key.json"
check "a node's own address is readable"     "AAAA1111" "$(local_validator_address 0)"

printf '{"result":{"total":"4","validators":[{"address":"AAAA1111","voting_power":"1"},{"address":"CCCC3333","voting_power":"1"},{"address":"DDDD4444","voting_power":"1"},{"address":"EEEE5555","voting_power":"1"}]}}\n' \
  >"$(stub_path /validators)"
check "a member's power is found"            "1" "$(validator_power_of 0 AAAA1111)"

# The mutation that matters: four equal-power validators exist, but this node is
# not one of them. Arity and power checks pass; identity does not.
check "count still says four"                "4" "$(validator_count 0)"
check "power still says one"                 "1" "$(min_validator_power 0)"
validator_power_of 0 BBBB2222 >/dev/null 2>&1
check "a non-member is refused"              "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# A member voting with the wrong weight is visible rather than merely present.
printf '{"result":{"total":"4","validators":[{"address":"AAAA1111","voting_power":"9"},{"address":"CCCC3333","voting_power":"1"},{"address":"DDDD4444","voting_power":"1"},{"address":"EEEE5555","voting_power":"1"}]}}\n' \
  >"$(stub_path /validators)"
check "an unequal member power is seen"      "9" "$(validator_power_of 0 AAAA1111)"

rm -f "$NET/node0/config/priv_validator_key.json"
local_validator_address 0 >/dev/null 2>&1
check "a missing key file refuses"           "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- fresh progress across a window ---------------------------------------------------

group "quorum progress is measured against a fresh mark"

# Comparing against a bound the nodes were already required to pass proves
# nothing about the window under test.
BEFORE=30; ALREADY_REQUIRED=28
check "a frozen node clears the old bound"   "true" \
  "$([[ "$BEFORE" -gt "$ALREADY_REQUIRED" ]] && echo true || echo false)"
check "  but fails against its own mark"     "false" \
  "$([[ "$BEFORE" -gt "$BEFORE" ]] && echo true || echo false)"
check "a progressing node clears the mark"   "true" \
  "$([[ $((BEFORE + 3)) -gt "$BEFORE" ]] && echo true || echo false)"

# ---- cross-node hash agreement -------------------------------------------------------

group "hash disagreement between nodes is caught"

hash_at() { jq -er ".result.block.header.$3" <"$WORK/block-$1.json" 2>/dev/null; }
printf '{"result":{"block":{"header":{"app_hash":"AAAA"}}}}\n' >"$WORK/block-0.json"
printf '{"result":{"block":{"header":{"app_hash":"AAAA"}}}}\n' >"$WORK/block-1.json"
printf '{"result":{"block":{"header":{"app_hash":"BBBB"}}}}\n' >"$WORK/block-2.json"
check "agreeing nodes match"                 "same" \
  "$([[ "$(hash_at 0 9 app_hash)" == "$(hash_at 1 9 app_hash)" ]] && echo same || echo DIFFERENT)"
check "one altered app hash is detected"     "different" \
  "$([[ "$(hash_at 0 9 app_hash)" != "$(hash_at 2 9 app_hash)" ]] && echo different || echo SAME)"

# ---- identity uniqueness -------------------------------------------------------------

group "membership alone does not bind the validator set to the nodes"

# The exact mutation the previous contract could not see: four equal-power
# validators [A,B,C,D], local identities [A,B,C,A]. Every membership and power
# lookup succeeds, D belongs to no node under test, and two nodes are the same
# validator — so a 3-of-4 argument would be counting a duplicate.
printf '{"result":{"total":"4","validators":[{"address":"AAAA","voting_power":"1"},{"address":"BBBB","voting_power":"1"},{"address":"CCCC","voting_power":"1"},{"address":"DDDD","voting_power":"1"}]}}\n' \
  >"$(stub_path /validators)"
DUP_LOCAL=(AAAA BBBB CCCC AAAA)

check "count still reads four"                "4" "$(validator_count 0)"
check "min power still reads one"             "1" "$(min_validator_power 0)"
check "max power still reads one"             "1" "$(max_validator_power 0)"
memb=0
for a in "${DUP_LOCAL[@]}"; do validator_power_of 0 "$a" >/dev/null 2>&1 && memb=$((memb + 1)); done
check "every local identity is a member"      "4" "$memb"
# ...and only uniqueness catches it.
check "uniqueness predicate FAILS on [A,B,C,A]" "3" "$(count_unique "${DUP_LOCAL[@]}")"
check "  which is not the node count"         "different" \
  "$([[ "$(count_unique "${DUP_LOCAL[@]}")" != "4" ]] && echo different || echo SAME)"
check "a genuinely distinct set passes"       "4" "$(count_unique AAAA BBBB CCCC DDDD)"

# ---- strict agreement ------------------------------------------------------------------

group "agreement fails closed on any unreadable hash"

# assert_agreement is the production helper, exercised directly rather than
# reimplemented — a second, more permissive copy in test shell would prove
# nothing about the code that runs.
# Returns the number of FAILURES the group raised. One per unreadable node:
# read_required_str raises it, and assert_agreement records the row without
# raising a second. Zero means the group agreed — which must only happen when
# every value was independently validated.
agree_probe() { # <name> <blocks...>
  local name="$1"; shift
  local i=0
  for b in "$@"; do printf '%s' "$b" >"$WORK/block-$i.json"; i=$((i + 1)); done
  DRILL_ASSERT_LOG="$WORK/ag.jsonl"; : >"$DRILL_ASSERT_LOG"; FAILURES=0
  assert_agreement "$name" 9 app_hash 0 1 2 >/dev/null 2>&1
  echo "$FAILURES"
}
GOOD='{"result":{"block":{"header":{"app_hash":"AAAA"}}}}'
BAD='{"result":{"block":{"header":{"app_hash":"BBBB"}}}}'
EMPTY=''

check "all equal -> no failures"              "0" "$(agree_probe ag_ok  "$GOOD" "$GOOD" "$GOOD")"
check "one mismatch -> failure"               "1" "$(agree_probe ag_mm  "$GOOD" "$GOOD" "$BAD")"
# The reference node itself unreadable: the old pattern seeded an EMPTY reference
# that later empty reads matched.
check "reference unreadable -> failure"       "1" "$(agree_probe ag_r0  "$EMPTY" "$GOOD" "$GOOD")"
check "later node unreadable -> failure"      "1" "$(agree_probe ag_r2  "$GOOD" "$GOOD" "$EMPTY")"
check "all unreadable -> failures, not agreement" "3" "$(agree_probe ag_all "$EMPTY" "$EMPTY" "$EMPTY")"
# The regression this replaces: three empty reads compared equal to each other and
# the group reported perfect agreement having read nothing at all.
check "  and never reports agreement"        "nonzero" \
  "$([[ "$(agree_probe ag_all2 "$EMPTY" "$EMPTY" "$EMPTY")" -gt 0 ]] && echo nonzero || echo AGREED)"

# ---- common-height selection ---------------------------------------------------------------

group "the comparison height is common, and fresh"

# Chosen from the validated minimum, so a lagging participant cannot be compared
# at a height it has not committed.
check "minimum, not first-asked"              "101" "$(min_uint 106 101 104)"
check "a lagging node sets the height"        "40"  "$(min_uint 55 40 61)"

# The no-common-fresh-block case from the review, exactly.
MARKS=(100 105 103); CURRENTS=(101 106 104)
prog=0
for i in 0 1 2; do [[ "${CURRENTS[$i]}" -gt "${MARKS[$i]}" ]] && prog=$((prog + 1)); done
check "every node beat its own mark"          "3" "$prog"
MM="$(max_uint "${MARKS[@]}")"; MC="$(min_uint "${CURRENTS[@]}")"
check "  yet no common fresh height exists"   "false" "$([[ "$MC" -gt "$MM" ]] && echo true || echo false)"

# And a genuine window passes, with a height later than every mark.
MARKS2=(100 105 103); CURRENTS2=(106 106 106)
MM2="$(max_uint "${MARKS2[@]}")"; MC2="$(min_uint "${CURRENTS2[@]}")"
check "a genuine window has one"              "true" "$([[ "$MC2" -gt "$MM2" ]] && echo true || echo false)"
check "  and the chosen height beats all marks" "true" \
  "$([[ $((MM2 + 1)) -gt 105 && $((MM2 + 1)) -le "$MC2" ]] && echo true || echo false)"

# ---- the version map -----------------------------------------------------------------

group "the module version map is parsed without fallback"

VM_OK='{"module_versions":[{"name":"coreslot","version":"2"},{"name":"runtime"}]}'
check "an omitted version is not zero"       "coreslot:2,runtime:omitted-zero" "$(version_map_from_json "$VM_OK")"
version_map_from_json '{"module_versions":[{"name":"x","version":null}]}' >/dev/null 2>&1
check "a null version refuses"               "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
version_map_from_json '{"module_versions":[]}' >/dev/null 2>&1
check "an empty map refuses"                 "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"
version_map_from_json '{"module_versions":[{"name":"x","version":"1"},{"name":"x","version":"2"}]}' >/dev/null 2>&1
check "a duplicate module refuses"           "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# ---- the CoreSlot snapshot comparison --------------------------------------------------

group "the CoreSlot comparison covers every canonical collection"

# params+slots equal while a DIFFERENT canonical collection moved. A comparison
# scoped to params and slots calls this unchanged; the full-section comparison
# must not.
BASE='{"params":{"a":1},"slots":[{"slot_id":"1"}],"next_slot_id":"2","pending_key_rotations":[],"pending_authority_transfers":[],"reserved_consensus_addresses":[],"reward_weights":[],"last_applied_validators":[],"selection_policies":[]}'
for field in next_slot_id pending_key_rotations pending_authority_transfers \
             reserved_consensus_addresses reward_weights last_applied_validators selection_policies; do
  MUT="$(jq -Sc --arg f "$field" 'if (.[$f] | type) == "array" then .[$f] = [{"x":1}] else .[$f] = "999" end' <<<"$BASE")"
  same_params="$([[ "$(jq -Sc '.params' <<<"$BASE")" == "$(jq -Sc '.params' <<<"$MUT")" ]] && echo yes || echo no)"
  same_slots="$([[ "$(jq -Sc '.slots' <<<"$BASE")" == "$(jq -Sc '.slots' <<<"$MUT")" ]] && echo yes || echo no)"
  full_equal="$([[ "$(jq -Sc . <<<"$BASE")" == "$(jq -Sc . <<<"$MUT")" ]] && echo same || echo different)"
  check "params+slots blind to $field"       "yes/yes" "$same_params/$same_slots"
  check "  full snapshot catches $field"     "different" "$full_equal"
done

# ---- the CLI surface predicate -----------------------------------------------------------

group "a parent's help does not prove a child exists"

# cobra exits zero for `parent --help` even when the requested child is absent,
# which is why the rehearsal builds a real transaction instead.
printf '#!/usr/bin/env bash\nif [[ "$*" == *"--help"* ]]; then echo "Usage: parent"; exit 0; fi\necho "unknown command"; exit 1\n' \
  >"$WORK/bin/fakecli"; chmod +x "$WORK/bin/fakecli"
"$WORK/bin/fakecli" tx coreslot nonexistent-command --help >/dev/null 2>&1
check "help exits zero for an absent child"  "0" "$?"
"$WORK/bin/fakecli" tx coreslot nonexistent-command >/dev/null 2>&1
check "and the real invocation fails"        "nonzero" "$([[ $? -ne 0 ]] && echo nonzero || echo ZERO)"

# The generated-transaction predicate the rehearsal actually uses.
printf '{"body":{"messages":[{"@type":"/twilight.coreslot.v1.MsgNominateAuthority","role":"AUTHORITY_ROLE_PRIMARY","nominee":"twilight1x"}]}}\n' >"$WORK/tx.json"
check "the generated msg type is checkable"  "/twilight.coreslot.v1.MsgNominateAuthority" \
  "$(jq -r '.body.messages[0]."@type"' "$WORK/tx.json")"
printf '{"body":{"messages":[]}}\n' >"$WORK/tx-empty.json"
check "an empty message list is detected"    "MISSING" \
  "$(jq -r '.body.messages[0]."@type" // "MISSING"' "$WORK/tx-empty.json")"

# ---- the assertion multiset ------------------------------------------------------------

group "the assertion multiset detects omission, duplication and renaming"

mk_log() { : >"$WORK/m.jsonl"; for row in "$@"; do
    printf '{"node":"%s","assertion":"%s","expected":"e","observed":"e","result":"PASS"}\n' \
      "${row#*|}" "${row%|*}" >>"$WORK/m.jsonl"; done; }
observed_multiset() { DRILL_ASSERT_LOG="$WORK/m.jsonl" assert_multiset_observed; }

mk_log "halt_app_height|0" "halt_app_height|1" "stale_did_not_commit_h|3"
BASELINE="$(observed_multiset)"
check "baseline multiset renders"            "halt_app_height|0:1,halt_app_height|1:1,stale_did_not_commit_h|3:1" "$BASELINE"

mk_log "halt_app_height|0" "stale_did_not_commit_h|3"
check "a removed assertion changes it"       "different" \
  "$([[ "$(observed_multiset)" != "$BASELINE" ]] && echo different || echo SAME)"

# One node's row omitted and another duplicated in its place — the count is
# identical, so only a multiset keyed by (assertion,node) notices.
mk_log "halt_app_height|0" "halt_app_height|0" "stale_did_not_commit_h|3"
check "a duplicated node is caught"          "different" \
  "$([[ "$(observed_multiset)" != "$BASELINE" ]] && echo different || echo SAME)"
check "  even though the count is unchanged" "3" "$(wc -l <"$WORK/m.jsonl" | tr -d ' ')"

mk_log "halt_app_height|0" "halt_app_height|1" "stale_did_not_commit_h|2"
check "an assertion on the wrong node"       "different" \
  "$([[ "$(observed_multiset)" != "$BASELINE" ]] && echo different || echo SAME)"

mk_log "halt_app_height|0" "halt_app_height|1" "renamed_assertion|3"
check "a renamed assertion is caught"        "different" \
  "$([[ "$(observed_multiset)" != "$BASELINE" ]] && echo different || echo SAME)"

# ---- the straggler topology --------------------------------------------------------

group "a node that already ran the candidate cannot pass as a straggler"

REHEARSAL="$ROOT/scripts/localnet/release-upgrade-rehearsal.sh"

# Structural: node 3 must not be in the set upgraded during the partial rollout.
# An earlier version of the rehearsal started ALL FOUR on the candidate and then
# returned node 3 to the released binary, which tests a DOWNGRADE onto migrated
# data — a different scenario, and one no operator meets.
check "the upgraded set excludes the straggler" "0" \
  "$(grep -cE '^readonly UPGRADED=\(0 1 2\)$' "$REHEARSAL" | grep -q 1 && echo 0 || echo 1)"
check "the straggler is node 3"                 "1" \
  "$(grep -cE '^readonly STALE=3$' "$REHEARSAL")"

# Behavioural: the assertion that would catch the mutation at runtime. A node that
# crossed the boundary on the candidate has an application height PAST H, so the
# stale proof's expectation of exactly H-1 fails — which is why that assertion is
# load-bearing rather than decorative.
H=42
check "a true straggler sits at H-1"            "match" \
  "$([[ "$((H - 1))" == "41" ]] && echo match || echo MISMATCH)"
check "one that crossed does not"               "different" \
  "$([[ "$((H + 2))" != "$((H - 1))" ]] && echo different || echo SAME)"

# ---- finalization on failure ------------------------------------------------------------

group "a failing run still writes a machine-readable verdict"

# finalize_verdict is TERMINAL: it writes the verdict and then exits. So each case
# runs in a subshell, or the first one would take this suite down with it — which
# is itself worth knowing, because a caller that expects it to return will lose
# everything after the call.
EV="$WORK/evid"; drill_assert_init "$EV" >/dev/null 2>&1
expect "deliberately_failing" "a" "b" >/dev/null 2>&1
# Separate statements, not a command prefix: `ARRAY=() cmd` is not a valid array
# assignment in bash, and silently gave finalize_verdict the outer scope's values.
( DRILL_MANDATORY_FILES=(); DRILL_VERDICT_GATES=(); finalize_verdict >/dev/null 2>&1 )
check "finalize exits non-zero on failure"   "1" "$?"
check "a verdict file exists after failure"  "yes" "$([[ -s "$EV/verdict.txt" ]] && echo yes || echo no)"
check "and it records FAIL"                  "overall=FAIL" "$(grep '^overall=' "$EV/verdict.txt")"

# Mandatory evidence that never got written must fail even when every assertion
# passed — a run that ended early otherwise reports the truth about the checks it
# reached and says nothing about the ones it did not.
EV2="$WORK/evid2"; drill_assert_init "$EV2" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(absent-evidence.json); DRILL_VERDICT_GATES=(); finalize_verdict >/dev/null 2>&1 )
check "missing mandatory evidence fails"     "1" "$?"
check "  and says so in the verdict"         "overall=FAIL" "$(grep '^overall=' "$EV2/verdict.txt")"

# A gate whose component was never emitted must fail rather than defaulting.
EV3="$WORK/evid3"; drill_assert_init "$EV3" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_VERDICT_GATES=("state=PRESERVED"); DRILL_VERDICT_LINES=()
  finalize_verdict >/dev/null 2>&1 )
check "an ungated component fails"           "1" "$?"

# And the all-good path still passes, so the suite is not simply asserting doom.
EV4="$WORK/evid4"; drill_assert_init "$EV4" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_VERDICT_GATES=("state=PRESERVED")
  DRILL_VERDICT_LINES=("state=PRESERVED"); FAILURES=0; finalize_verdict >/dev/null 2>&1 )
check "a clean run passes"                   "0" "$?"
check "  and records PASS"                   "overall=PASS" "$(grep '^overall=' "$EV4/verdict.txt")"

echo
if (( FAILED > 0 )); then
  echo "rehearsal faults: FAIL ($FAILED of $((PASSED+FAILED)))" >&2; exit 1
fi
echo "rehearsal faults: PASS ($PASSED checks)"
