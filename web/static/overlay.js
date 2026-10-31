// Incrustation du chat Twitch : affiche les messages en direct, avec leurs badges.
// Tout le texte passe par textContent : le contenu du chat n'est jamais interprété comme du HTML.
(() => {
  const token = new URLSearchParams(location.search).get("token") || "";
  const query = "?token=" + encodeURIComponent(token);
  const list = document.getElementById("chat");

  let badges = {};
  let settings = { max_messages: 40, font_size: 22, show_badges: true };

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
      // Réseau indisponible : on garde les valeurs par défaut.
    }
    return null;
  }

  async function start() {
    const [loadedBadges, loadedSettings] = await Promise.all([
      getJSON("/overlay/badges"),
      getJSON("/overlay/settings"),
    ]);
    if (loadedBadges) badges = loadedBadges;
    if (loadedSettings) settings = loadedSettings;
    document.documentElement.style.setProperty("--font-size", settings.font_size + "px");

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
