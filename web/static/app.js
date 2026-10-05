"use strict";

// Tout le contenu dynamique est ajouté via textContent / createElement :
// jamais d'innerHTML, donc pas d'injection possible depuis un nom de commande,
// une réponse ou un pseudo de spectateur.

const $ = (id) => document.getElementById(id);

function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v);
  }
  for (const c of children) el.append(c);
  return el;
}

let toastTimer;
function toast(message, kind) {
  const el = $("toast");
  el.textContent = message;
  el.className = "toast " + (kind || "");
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 4000);
}

async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch (_) { /* corps vide */ }
  if (!res.ok) {
    throw new Error((data && data.error) || "Erreur " + res.status);
  }
  return data;
}

const LEVELS = [
  ["everyone", "Tout le monde"],
  ["subscriber", "Abonnés et plus"],
  ["vip", "VIP et plus"],
  ["moderator", "Modérateurs et plus"],
  ["broadcaster", "Streamer"],
];
const levelLabel = Object.fromEntries(LEVELS);

for (const sel of document.querySelectorAll("select[data-level]")) {
  for (const [value, label] of LEVELS) sel.append(h("option", { value }, label));
}

// ---- statut ----

function pill(id, text, kind) {
  const el = $(id);
  el.hidden = false;
  el.textContent = text;
  el.className = "pill " + kind;
}

// Affiche l'état d'un compte externe et le bouton de connexion ou de déconnexion.
function renderAccount(name, label, st, disconnect) {
  const action = $(name + "-action");
  action.replaceChildren();
  if (!st.configured) {
    pill(name + "-pill", label + " : non configuré", "warn");
  } else if (st.connected) {
    pill(name + "-pill", label + " : " + (st.account || "connecté"), "ok");
    action.append(h("button", { class: "secondary small", type: "button", onclick: disconnect }, "Déconnecter"));
  } else {
    pill(name + "-pill", label + " : non connecté", "warn");
    action.append(h("a", { class: "button small", href: "/auth/" + name + "/login" }, "Connecter"));
  }
}

async function refreshStatus() {
  try {
    const s = await api("GET", "/api/status");
    const bot = s.bot_connected ? "connecté à #" + s.channel : "hors ligne" + (s.bot_error ? " (" + s.bot_error + ")" : "");
    pill("bot-pill", "Bot : " + bot, s.bot_connected ? "ok" : "bad");
    // Le compte Twitch n'apparaît que si la connexion OAuth est configurée (sinon : jeton fixe).
    if (s.twitch.configured) renderAccount("twitch", "Twitch", s.twitch, () => disconnect("twitch", "Twitch", "Le bot quittera le chat jusqu'à la prochaine connexion."));
    renderAccount("spotify", "Spotify", s.spotify, () => disconnect("spotify", "Spotify", "Les demandes de musique seront désactivées."));
  } catch (e) {
    pill("bot-pill", "Bot : injoignable", "bad");
  }
}

async function disconnect(name, label, consequence) {
  if (!confirm("Déconnecter le compte " + label + " ? " + consequence)) return;
  try {
    await api("POST", "/api/" + name + "/disconnect");
    toast(label + " déconnecté", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  refreshStatus();
}

// ---- commandes ----

let editing = null; // nom de la commande en cours de modification

function updateArgsWarning() {
  const risky = $("cmd-response").value.includes("{args}") && $("cmd-permission").value === "everyone";
  $("args-warning").hidden = !risky;
}

function resetForm() {
  editing = null;
  $("cmd-form").reset();
  $("cmd-name").disabled = false;
  $("cmd-cooldown").value = 5;
  $("cmd-user-cooldown").value = 0;
  $("cmd-enabled").checked = true;
  $("cmd-submit").textContent = "Ajouter";
  $("cmd-cancel").hidden = true;
  updateArgsWarning();
}

function startEdit(cmd) {
  editing = cmd.name;
  $("cmd-name").value = cmd.name;
  $("cmd-name").disabled = true; // le nom est la clé : on ne le renomme pas
  $("cmd-response").value = cmd.response;
  $("cmd-permission").value = cmd.permission;
  $("cmd-aliases").value = cmd.aliases.join(" ");
  $("cmd-cooldown").value = cmd.cooldown_seconds;
  $("cmd-user-cooldown").value = cmd.user_cooldown_seconds;
  $("cmd-enabled").checked = cmd.enabled;
  $("cmd-submit").textContent = "Enregistrer";
  $("cmd-cancel").hidden = false;
  $("cmd-response").focus();
  updateArgsWarning();
}

function commandSummary(c) {
  const parts = ["délai : " + c.cooldown_seconds + " s"];
  if (c.user_cooldown_seconds > 0) parts.push(c.user_cooldown_seconds + " s par spectateur");
  if (c.permission !== "everyone") parts.push(levelLabel[c.permission]);
  if (c.aliases.length) parts.push("alias : " + c.aliases.map((a) => "!" + a).join(" "));
  parts.push(c.use_count + " utilisation" + (c.use_count > 1 ? "s" : ""));
  return c.response + "  (" + parts.join(" · ") + ")";
}

async function loadCommands() {
  const list = $("commands");
  try {
    const cmds = await api("GET", "/api/commands");
    list.replaceChildren();
    if (cmds.length === 0) {
      list.append(h("li", { class: "empty" }, "Aucune commande"));
      return;
    }
    for (const c of cmds) {
      list.append(h("li", { class: c.enabled ? "" : "disabled" },
        h("div", { class: "item-main" },
          h("div", { class: "item-title" }, "!" + c.name + (c.enabled ? "" : " (désactivée)")),
          h("div", { class: "item-sub" }, commandSummary(c))),
        h("div", { class: "item-actions" },
          h("button", { class: "secondary small", type: "button", onclick: () => startEdit(c) }, "Modifier"),
          h("button", { class: "danger small", type: "button", onclick: () => deleteCommand(c.name) }, "Supprimer"))));
    }
  } catch (e) {
    toast(e.message, "bad");
  }
}

async function deleteCommand(name) {
  if (!confirm("Supprimer la commande !" + name + " ?")) return;
  try {
    await api("DELETE", "/api/commands/" + encodeURIComponent(name));
    if (editing === name) resetForm();
    toast("Commande supprimée", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  loadCommands();
}

function intOr(id, fallback) {
  const n = parseInt($(id).value, 10);
  return Number.isNaN(n) ? fallback : n;
}

$("cmd-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const body = {
    response: $("cmd-response").value,
    cooldown_seconds: intOr("cmd-cooldown", 5),
    user_cooldown_seconds: intOr("cmd-user-cooldown", 0),
    permission: $("cmd-permission").value,
    enabled: $("cmd-enabled").checked,
    aliases: $("cmd-aliases").value.split(/[\s,]+/).filter(Boolean),
  };
  try {
    if (editing) {
      await api("PUT", "/api/commands/" + encodeURIComponent(editing), body);
      toast("Commande modifiée", "ok");
    } else {
      await api("POST", "/api/commands", { name: $("cmd-name").value, ...body });
      toast("Commande ajoutée", "ok");
    }
    resetForm();
  } catch (e) {
    toast(e.message, "bad");
  }
  loadCommands();
});

$("cmd-cancel").addEventListener("click", resetForm);
$("cmd-response").addEventListener("input", updateArgsWarning);
$("cmd-permission").addEventListener("change", updateArgsWarning);

// ---- réglages de la musique ----

async function loadMusicSettings() {
  try {
    const m = await api("GET", "/api/music-settings");
    $("m-request-command").value = m.request_command;
    $("m-request-aliases").value = (m.request_aliases || []).join(" ");
    const label = "!" + m.request_command;
    $("m-cmd-hint").textContent = label;
    $("req-cmd-hint").textContent = label;
    $("m-request-level").value = m.request_level;
    $("m-skip-level").value = m.skip_level;
    $("m-max-pending").value = m.max_pending;
    $("m-max-per-user").value = m.max_per_user;
    $("m-max-duration").value = m.max_duration_seconds;
    $("m-block-explicit").checked = m.block_explicit;
    $("m-blocklist").value = m.blocklist.join("\n");
  } catch (e) {
    toast(e.message, "bad");
  }
}

$("music-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  try {
    await api("PUT", "/api/music-settings", {
      request_command: $("m-request-command").value,
      request_aliases: $("m-request-aliases").value.split(/[\s,]+/).filter(Boolean),
      request_level: $("m-request-level").value,
      skip_level: $("m-skip-level").value,
      max_pending: intOr("m-max-pending", 0),
      max_per_user: intOr("m-max-per-user", 0),
      max_duration_seconds: intOr("m-max-duration", 0),
      block_explicit: $("m-block-explicit").checked,
      blocklist: $("m-blocklist").value.split("\n"),
    });
    toast("Règles enregistrées", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  loadMusicSettings();
});

// ---- file Spotify ----

function duration(sec) {
  return Math.floor(sec / 60) + ":" + String(sec % 60).padStart(2, "0");
}

function trackLabel(t) {
  return t.artist + " – " + t.name + (t.explicit ? " 🅴" : "") + "  (" + duration(t.duration_seconds) + ")";
}

async function loadQueue() {
  const list = $("q-list");
  try {
    const q = await api("GET", "/api/spotify/queue");
    $("q-current").textContent = q.current ? "En cours : " + trackLabel(q.current) : "Rien en cours de lecture";
    list.replaceChildren();
    for (const t of q.queue) {
      list.append(h("li", {}, h("div", { class: "item-main" }, h("div", { class: "item-sub" }, trackLabel(t)))));
    }
  } catch (e) {
    $("q-current").textContent = e.message;
    list.replaceChildren();
  }
}

$("q-refresh").addEventListener("click", loadQueue);
$("q-skip").addEventListener("click", async () => {
  try {
    await api("POST", "/api/spotify/skip");
    toast("Titre passé", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  setTimeout(loadQueue, 600); // laisse à Spotify le temps de changer de titre
});

// ---- demandes de musique ----

function formatTime(unix) {
  return new Date(unix * 1000).toLocaleString("fr-BE", { dateStyle: "short", timeStyle: "short" });
}

async function loadRequests() {
  const list = $("requests");
  try {
    const reqs = await api("GET", "/api/requests?limit=50");
    list.replaceChildren();
    if (reqs.length === 0) {
      list.append(h("li", { class: "empty" }, "Aucune demande"));
      return;
    }
    for (const r of reqs) {
      const failed = r.status !== "queued";
      list.append(h("li", { class: failed ? "failed" : "" },
        h("div", { class: "item-main" },
          h("div", { class: "item-title" }, r.artist + " – " + r.track_name),
          h("div", { class: "item-sub" },
            "par " + r.requested_by + " · " + formatTime(r.created_at) + (failed ? " · échec" : ""))),
        h("div", { class: "item-actions" },
          h("button", { class: "secondary small", type: "button", onclick: () => deleteRequest(r.id) }, "Retirer"))));
    }
  } catch (e) {
    toast(e.message, "bad");
  }
}

async function deleteRequest(id) {
  try {
    await api("DELETE", "/api/requests/" + id);
  } catch (e) {
    toast(e.message, "bad");
  }
  loadRequests();
}

$("req-refresh").addEventListener("click", loadRequests);
$("req-clear").addEventListener("click", async () => {
  if (!confirm("Vider tout l'historique des demandes ?")) return;
  try {
    await api("DELETE", "/api/requests");
    toast("Historique vidé", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  loadRequests();
});

// ---- démarrage ----

(function showOAuthResult() {
  const params = new URLSearchParams(location.search);
  const messages = {
    connected: ["Compte connecté", "ok"],
    denied: ["Connexion refusée", "bad"],
    error: ["Échec de la connexion (voir les logs)", "bad"],
  };
  let shown = false;
  for (const name of ["spotify", "twitch"]) {
    const p = params.get(name);
    if (p && messages[p]) {
      toast((name === "spotify" ? "Spotify : " : "Twitch : ") + messages[p][0], messages[p][1]);
      shown = true;
    }
  }
  if (shown) history.replaceState(null, "", location.pathname);
})();

refreshStatus();
loadCommands();
loadMusicSettings();
loadQueue();
loadRequests();
setInterval(refreshStatus, 5000);
setInterval(loadQueue, 15000);
setInterval(loadRequests, 10000);
