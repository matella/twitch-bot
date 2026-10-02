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

// ---- statut ----

function pill(id, text, kind) {
  const el = $(id);
  el.textContent = text;
  el.className = "pill " + kind;
}

async function refreshStatus() {
  try {
    const s = await api("GET", "/api/status");
    pill("bot-pill", "Bot : " + (s.bot_connected ? "connecté à #" + s.channel : "hors ligne"), s.bot_connected ? "ok" : "bad");

    const action = $("spotify-action");
    action.replaceChildren();
    if (!s.spotify.configured) {
      pill("spotify-pill", "Spotify : non configuré", "warn");
    } else if (s.spotify.connected) {
      pill("spotify-pill", "Spotify : " + (s.spotify.account || "connecté"), "ok");
      action.append(h("button", { class: "secondary small", type: "button", onclick: disconnectSpotify }, "Déconnecter"));
    } else {
      pill("spotify-pill", "Spotify : non connecté", "warn");
      action.append(h("a", { class: "button small", href: "/auth/spotify/login" }, "Connecter Spotify"));
    }
  } catch (e) {
    pill("bot-pill", "Bot : injoignable", "bad");
  }
}

async function disconnectSpotify() {
  if (!confirm("Déconnecter le compte Spotify ? Les demandes de musique seront désactivées.")) return;
  try {
    await api("POST", "/api/spotify/disconnect");
    toast("Spotify déconnecté", "ok");
  } catch (e) {
    toast(e.message, "bad");
  }
  refreshStatus();
}

// ---- commandes ----

let editing = null; // nom de la commande en cours de modification

function resetForm() {
  editing = null;
  $("cmd-form").reset();
  $("cmd-name").disabled = false;
  $("cmd-cooldown").value = 5;
  $("cmd-submit").textContent = "Ajouter";
  $("cmd-cancel").hidden = true;
}

function startEdit(cmd) {
  editing = cmd.name;
  $("cmd-name").value = cmd.name;
  $("cmd-name").disabled = true; // le nom est la clé : on ne le renomme pas
  $("cmd-response").value = cmd.response;
  $("cmd-cooldown").value = cmd.cooldown_seconds;
  $("cmd-submit").textContent = "Enregistrer";
  $("cmd-cancel").hidden = false;
  $("cmd-response").focus();
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
      list.append(h("li", {},
        h("div", { class: "item-main" },
          h("div", { class: "item-title" }, "!" + c.name),
          h("div", { class: "item-sub" }, c.response + "  (délai : " + c.cooldown_seconds + " s)")),
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

$("cmd-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const response = $("cmd-response").value;
  const cooldown = parseInt($("cmd-cooldown").value, 10);
  const body = { response, cooldown_seconds: Number.isNaN(cooldown) ? 5 : cooldown };
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

(function showSpotifyResult() {
  const p = new URLSearchParams(location.search).get("spotify");
  if (!p) return;
  const messages = {
    connected: ["Compte Spotify connecté", "ok"],
    denied: ["Connexion Spotify refusée", "bad"],
    error: ["Échec de la connexion Spotify (voir les logs)", "bad"],
  };
  if (messages[p]) toast(...messages[p]);
  history.replaceState(null, "", location.pathname);
})();

refreshStatus();
loadCommands();
loadRequests();
setInterval(refreshStatus, 5000);
setInterval(loadRequests, 10000);
