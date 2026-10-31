// Incrustation du chat Twitch : affiche les messages en direct, avec leurs badges.
// Les réglages sont relus toutes les quelques secondes : un changement dans l'administration
// s'applique sans recharger la source OBS.
// Tout le texte passe par textContent : le contenu du chat n'est jamais interprété comme du HTML.
(() => {
  const SETTINGS_EVERY_MS = 3000;
  const FONTS = {
    sans: '"Segoe UI", system-ui, sans-serif',
    serif: 'Georgia, "Times New Roman", serif',
    mono: '"Consolas", "Courier New", monospace',
  };
  const DEFAULTS = {
    max_messages: 40,
    font_size: 22,
    font_family: "sans",
    text_color: "#ffffff",
    show_badges: true,
    position: "bottom-left",
    margin: 12,
    width: 600,
  };

  const token = new URLSearchParams(location.search).get("token") || "";
  const query = "?token=" + encodeURIComponent(token);
  const list = document.getElementById("chat");

  let badges = {};
  let settings = DEFAULTS;
  let appliedJSON = "";

  // Applique les réglages à la page ; ne fait rien si rien n'a changé.
  function applySettings(s) {
    const json = JSON.stringify(s);
    if (json === appliedJSON) return;
    appliedJSON = json;
    settings = s;

    const style = document.documentElement.style;
    style.setProperty("--font-size", s.font_size + "px");
    style.setProperty("--font-family", FONTS[s.font_family] || FONTS.sans);
    style.setProperty("--text-color", s.text_color);
    style.setProperty("--margin", s.margin + "px");
    style.setProperty("--width", s.width + "px");
    list.className = "pos-" + s.position;

    // Si le nombre de messages à l'écran a baissé, on retire les plus anciens.
    while (list.children.length > s.max_messages) list.firstElementChild.remove();
  }

  // Badge sans image (catalogue indisponible ou image en erreur) : le nom du badge, en petit.
  function badgeText(key) {
    const span = document.createElement("span");
    span.className = "badge-text";
    span.textContent = key.split("/")[0];
    return span;
  }

  function badgeElement(key) {
    const url = badges[key];
    if (!url) return badgeText(key);
    const img = document.createElement("img");
    img.className = "badge";
    img.src = url;
    img.alt = "";
    img.title = key.split("/")[0];
    img.onerror = () => img.replaceWith(badgeText(key));
    return img;
  }

  function render(m) {
    const li = document.createElement("li");
    li.className = "msg";

    if (settings.show_badges) {
      for (const key of m.badges || []) li.append(badgeElement(key));
    }

    const name = document.createElement("span");
    name.className = "name";
    name.textContent = m.name;
    if (m.color) name.style.color = m.color;

    const text = document.createElement("span");
    text.className = "text";
    text.textContent = m.text;

    li.append(name, ": ", text);
    list.append(li);
    while (list.children.length > settings.max_messages) list.firstElementChild.remove();
  }

  async function getJSON(path) {
    try {
      const res = await fetch(path + query);
      if (res.ok) return await res.json();
    } catch (_) {
      // Réseau indisponible : on garde ce qui est déjà affiché.
    }
    return null;
  }

  async function refreshSettings() {
    const s = await getJSON("/overlay/settings");
    if (s) applySettings(s);
  }

  async function start() {
    applySettings(DEFAULTS);
    const [loadedBadges] = await Promise.all([getJSON("/overlay/badges"), refreshSettings()]);
    if (loadedBadges) badges = loadedBadges;
    setInterval(refreshSettings, SETTINGS_EVERY_MS);

    // EventSource se reconnecte seul après une coupure.
    const source = new EventSource("/overlay/events" + query);
    source.onmessage = (e) => {
      try {
        render(JSON.parse(e.data));
      } catch (_) {
        // Message mal formé : on l'ignore.
      }
    };
  }

  start();
})();
