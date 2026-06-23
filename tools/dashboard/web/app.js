// Twilight devnet dashboard — fetches the Go backend's /api/* JSON and renders
// the chain's custom rewards + CoreSlot state, auto-refreshing.

const $ = (id) => document.getElementById(id);
const j = async (p) => (await fetch(p)).json();

// utwlt -> grouped string; also TWLT (1 TWLT = 1e6 utwlt) for big values.
const grp = (n) => (n ?? "0").toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
const twlt = (u) => (Number(u || 0) / 1e6).toLocaleString(undefined, { maximumFractionDigits: 6 });
const short = (a) => (a ? a.slice(0, 12) + "…" + a.slice(-6) : "—");

function card(tag, title, value, sub) {
  return `<div class="card"><div class="k">${tag}</div><div class="v">${value}</div>${
    sub ? `<div class="k" style="margin-top:.3rem;text-transform:none;letter-spacing:0">${sub}</div>` : ""
  }${title ? "" : ""}</div>`;
}

function bar(pct) {
  pct = Math.max(0, Math.min(100, pct));
  return `<div style="height:8px;background:#26262c;border-radius:6px;overflow:hidden;margin-top:.5rem">
    <div style="height:100%;width:${pct}%;background:var(--accent)"></div></div>`;
}

async function tick() {
  let ov, pa, va, cl, bl, tx;
  try {
    [ov, pa, va, cl, bl, tx] = await Promise.all([
      j("/api/overview"), j("/api/params"), j("/api/validators"), j("/api/claims"),
      j("/api/blocks?limit=12").catch(() => ({ blocks: [] })),
      j("/api/txs?heights=200&limit=25").catch(() => ({ txs: [] })),
    ]);
  } catch (e) {
    $("updated").textContent = "backend unreachable";
    return;
  }

  $("chain").textContent = ov.chain_id || "devnet";
  $("updated").textContent = "updated " + new Date().toLocaleTimeString() + " · height " + (ov.height ?? "?");

  const st = ov.epoch?.state || {};
  const hv = pa.next_halving?.info || {};
  const rp = pa.rewards?.params || {};
  const cs = pa.coreslot?.params || {};
  const supply = ov.supply?.amount?.amount || "0";
  const maxSupply = hv.max_supply || rp.max_supply || "0";
  const cum = ov.cumulative?.cumulative_emitted || st.cumulative_emitted || "0";
  const modBal = ov.module_balances?.rewards_balance || "0";
  const supplyPct = (Number(cum) / Number(maxSupply || 1)) * 100;
  const halvePct = hv.next_threshold ? (Number(hv.cumulative_emitted) / Number(hv.next_threshold)) * 100 : 0;

  let html = "";

  // --- overview cards ---
  html += card("Current epoch", "", "#" + (st.current_epoch ?? "—"),
    `blocks ${grp(st.current_epoch_start_height)}–${grp(ov.epoch?.current_epoch_end_height)}`);
  html += card("Total supply", "", grp(supply) + " <small style='font-size:.6em;color:var(--muted)'>utwlt</small>",
    twlt(supply) + " TWLT");
  html += card("Cumulative emitted", "", grp(cum), twlt(cum) + " TWLT");
  html += card("Rewards module balance", "", grp(modBal), "unclaimed + carry");
  html += card("Block subsidy", "", grp(hv.current_block_subsidy || rp.initial_block_subsidy), "per block · tier " + (hv.current_tier ?? "0"));
  html += card("Active validators", "", (ov.active_slots?.slots?.length ?? va.active?.slots?.length ?? 0),
    "CoreSlot PoA");
  html += card("Epoch length", "", grp(rp.epoch_length_blocks), "blocks");
  html += card("Pending params", "", ov.epoch?.has_pending_params ? "yes" : "no", "");

  // --- emission / halving (wide) ---
  html += `<div class="card wide"><div class="k">Emission & halving</div>
    <div style="display:flex;gap:2rem;flex-wrap:wrap;margin-top:.4rem">
      <div><div class="k">Max supply</div><div class="v" style="font-size:1.1rem">${grp(maxSupply)} <small style="color:var(--muted)">utwlt</small></div></div>
      <div><div class="k">Halving tier</div><div class="v" style="font-size:1.1rem">${hv.current_tier ?? 0}</div></div>
      <div><div class="k">Next threshold</div><div class="v" style="font-size:1.1rem">${grp(hv.next_threshold)}</div></div>
      <div><div class="k">Until next halving</div><div class="v" style="font-size:1.1rem">${grp(hv.remaining_until_next_halving)}</div></div>
    </div>
    <div class="k" style="margin-top:.8rem;text-transform:none;letter-spacing:0">Progress to next threshold (${halvePct.toFixed(2)}%)</div>${bar(halvePct)}
    <div class="k" style="margin-top:.6rem;text-transform:none;letter-spacing:0">Progress to max supply (${supplyPct.toFixed(4)}%)</div>${bar(supplyPct)}
  </div>`;

  // --- validators (wide) ---
  const slots = va.slots?.slots || [];
  html += `<div class="card wide"><div class="k">CoreSlot validators (${slots.length})</div>
    <table><thead><tr><th>slot</th><th>moniker</th><th>operator</th><th>payout</th><th>status</th><th>power</th><th>weight</th></tr></thead><tbody>
    ${slots.map((s) => `<tr>
      <td class="pill">${s.slot_id}</td>
      <td>${s.metadata?.moniker || "—"}</td>
      <td title="${s.operator_address}">${short(s.operator_address)}</td>
      <td title="${s.payout_address}">${short(s.payout_address)}</td>
      <td>${(s.status || "").replace("SLOT_STATUS_", "")}</td>
      <td>${s.consensus_power ?? "—"}</td>
      <td>${s.reward_weight ?? "—"}</td></tr>`).join("")}
    </tbody></table></div>`;

  // --- claims (wide) ---
  const rows = [];
  (cl.by_slot || []).forEach((r) => (r.rewards || []).forEach((rw) => rows.push(rw)));
  rows.sort((a, b) => Number(b.epoch_number) - Number(a.epoch_number));
  html += `<div class="card wide"><div class="k">Reward records (${rows.length}) — latest first</div>
    <table><thead><tr><th>epoch</th><th>payout</th><th>amount</th><th>blocks active</th><th>claimed</th></tr></thead><tbody>
    ${rows.slice(0, 25).map((r) => `<tr>
      <td class="pill">#${r.epoch_number}</td>
      <td title="${r.payout_address}">${short(r.payout_address)}</td>
      <td>${grp(r.amount)}</td>
      <td>${r.blocks_active ?? "—"}</td>
      <td style="color:${r.claimed ? "var(--accent)" : "var(--muted)"}">${r.claimed ? "claimed" : "unclaimed"}</td></tr>`).join("")}
    </tbody></table>${rows.length === 0 ? '<div class="k" style="text-transform:none">no finalized epochs yet</div>' : ""}</div>`;

  // --- operators / registration helper (wide) ---
  html += `<div class="card wide"><div class="k">Onboard an operator (authority action)</div>
    <div class="k" style="text-transform:none;letter-spacing:0;margin:.4rem 0">
      authority: <span class="pill" title="${cs.authority}">${short(cs.authority)}</span> ·
      emergency: <span class="pill" title="${cs.emergency_authority}">${short(cs.emergency_authority)}</span> ·
      max active slots: ${cs.max_active_slots ?? "—"}
    </div>
    <pre style="background:#0c0c0f;border:1px solid var(--border);border-radius:8px;padding:.8rem;overflow:auto;font-size:.78rem;color:#cfcfe0">H=~/.twilight-devnet; C="--from validator --keyring-backend test --home $H --chain-id ${ov.chain_id} --node http://localhost:26657 --gas 400000 --fees 0utwlt -y"
twilightd coreslot register &lt;operator&gt; &lt;payout&gt; &lt;consensus-pubkey-b64&gt; "&lt;moniker&gt;" $C
twilightd coreslot-query slots --node http://localhost:26657 -o json   # find new slot-id
twilightd coreslot activate &lt;slot-id&gt; $C</pre>
    <div class="k" style="text-transform:none;letter-spacing:0">Operator gets their pubkey with <code>twilightd comet show-validator | jq -r .key</code>. See devnet/README.md.</div>
  </div>`;

  // --- blocks (wide) ---
  const blocks = bl?.blocks || [];
  html += `<div class="card wide"><div class="k">Latest blocks</div>
    <table><thead><tr><th>height</th><th>time</th><th>proposer</th><th>txs</th><th>app hash</th></tr></thead><tbody>
    ${blocks.map((b) => `<tr>
      <td class="pill">${grp(b.height)}</td>
      <td>${b.time ? new Date(b.time).toLocaleTimeString() : "—"}</td>
      <td title="${b.proposer}">${(b.proposer || "").slice(0, 12)}…</td>
      <td>${b.num_txs ?? 0}</td>
      <td title="${b.app_hash}">${(b.app_hash || "").slice(0, 12)}…</td></tr>`).join("")}
    </tbody></table></div>`;

  // --- transactions (wide) — decoded with the chain codec (custom Msgs included) ---
  const txs = tx?.txs || [];
  html += `<div class="card wide"><div class="k">Transactions (${txs.length}) — decoded</div>
    <table><thead><tr><th>height</th><th>hash</th><th>status</th><th>messages</th><th>details</th></tr></thead><tbody>
    ${txs.map((t) => {
      const types = (t.messages || []).map((m) => m.type).join(", ") || (t.decode_error ? "undecodable tx" : "—");
      const detail = t.decode_error
        ? `<details><summary>raw + error</summary><pre style="white-space:pre-wrap;font-size:.72rem;color:#f6a">${t.decode_error}\n${(t.raw || "").slice(0, 120)}…</pre></details>`
        : `<details><summary>decoded JSON</summary><pre style="white-space:pre-wrap;font-size:.72rem;color:#cfcfe0">${(t.messages || [])
            .map((m) => m.error ? `${m.type}\n  ⚠ ${m.error}` : `${m.type}\n${JSON.stringify(m.body, null, 1)}`)
            .join("\n\n")}</pre></details>`;
      return `<tr>
        <td class="pill">${grp(t.height)}</td>
        <td title="${t.hash}">${(t.hash || "").slice(0, 12)}…</td>
        <td style="color:${t.success ? "var(--accent)" : "#f6a"}">${t.success ? "ok" : "code " + (t.code ?? "?")}</td>
        <td style="font-size:.78rem">${types}</td>
        <td>${detail}</td></tr>`;
    }).join("")}
    </tbody></table>${txs.length === 0 ? '<div class="k" style="text-transform:none">no transactions in the scanned range</div>' : ""}</div>`;

  $("cards").innerHTML = html;
}

tick();
setInterval(tick, 5000);
