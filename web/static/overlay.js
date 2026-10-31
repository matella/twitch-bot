// Incrustation du chat Twitch : affiche les messages en direct, avec leurs badges.
// Tout le texte passe par textContent : le contenu du chat n'est jamais interprété comme du HTML.
(() => {
  const MAX_MESSAGES = 40;

  const token = new URLSearchParams(location.search).get("token") || "";
  const query = "?token=" + encodeURIComponent(token);
  const list = document.getElementById("chat");
  let badges = {};

  function badgeImage(key) {
    const url = badges[key];
    if (!url) return null;
    const img = document.createElement("img");
    img.className = "badge";
    img.src = url;
    img.alt = "";
    img.title = key.split("/")[0];
    return img;
  }

  function render(m) {
    const li = document.createElement("li");
    li.className = "msg";

    for (const key of m.badges || []) {
      const img = badgeImage(key);
      if (img) li.append(img);
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
    while (list.children.length > MAX_MESSAGES) list.firstElementChild.remove();
  }

  async function start() {
    try {
      const res = await fetch("/overlay/badges" + query);
      if (res.ok) badges = await res.json();
    } catch (_) {
      // Sans badges, le chat reste lisible.
    }
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
