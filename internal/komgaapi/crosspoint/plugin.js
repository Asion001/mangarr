// Mangarr setup for the reader. Collects the server address, a reading-app
// device key and the screen size in the browser and writes them to
// config.json in the plugin folder, which the on-device catalog (device.json)
// reads to browse, download and send progress.
CrossPoint.registerPlugin(async (container, api) => {
  const CONFIG_PATH = api.dir + '/config.json';
  const SCREENS = [
    ['480x800', 'Xteink X4 (480 × 800)'],
    ['528x792', 'Xteink X3 (528 × 792)'],
    ['custom', 'Other…'],
  ];

  container.innerHTML =
    '<h2>Mangarr</h2>' +
    '<p id="mg-status">Checking configuration…</p>' +
    '<div class="setting-row"><label class="setting-name" for="mg-server">Server address</label>' +
    '<span class="setting-control"><input type="url" id="mg-server" placeholder="http://192.168.1.10:25600"></span></div>' +
    '<div class="setting-row"><label class="setting-name" for="mg-key">Device key</label>' +
    '<span class="setting-control"><input type="password" id="mg-key" autocomplete="off"></span></div>' +
    '<div class="setting-row"><label class="setting-name" for="mg-screen">Screen</label>' +
    '<span class="setting-control"><select id="mg-screen">' +
    SCREENS.map(([v, l]) => '<option value="' + v + '">' + l + '</option>').join('') +
    '</select> <input type="text" id="mg-custom" placeholder="WIDTHxHEIGHT" style="display:none;width:8em"></span></div>' +
    '<div class="setting-row"><label class="setting-name" for="mg-webtoons">Show webtoons</label>' +
    '<span class="setting-control"><input type="checkbox" id="mg-webtoons"></span></div>' +
    '<div class="setting-row">' +
    '<button type="button" class="btn-small btn-add" id="mg-save">Save</button> ' +
    '<button type="button" class="btn-small" id="mg-test">Test</button>' +
    '</div>' +
    '<p style="color:#666">Use the reading-app port (25600 by default) and a device key from ' +
    'My account → Reading apps. The key is stored in plain text on the SD card.</p>';

  const $ = (id) => document.getElementById(id);
  const status = (t) => { $('mg-status').textContent = t; };
  const screenSel = $('mg-screen');
  const customEl = $('mg-custom');
  screenSel.onchange = () => { customEl.style.display = screenSel.value === 'custom' ? '' : 'none'; };

  async function loadConfig() {
    try {
      const r = await fetch('/download?path=' + encodeURIComponent(CONFIG_PATH));
      if (!r.ok) return null;
      return JSON.parse(await r.text());
    } catch (e) {
      return null;
    }
  }

  function currentConfig() {
    const server = $('mg-server').value.trim().replace(/\/+$/, '');
    const key = $('mg-key').value.trim();
    if (!/^https?:\/\//.test(server)) throw new Error('the server address starts with http:// or https://');
    if (!key) throw new Error('a device key is required');
    let screen = screenSel.value;
    if (screen === 'custom') {
      screen = customEl.value.trim().toLowerCase().replace(/\s/g, '');
      if (!/^\d{3,4}x\d{3,4}$/.test(screen)) throw new Error('the screen size is WIDTHxHEIGHT, like 480x800');
    }
    // Every key is always written: the device sends a missing {cfg.KEY} literally.
    return { server, key, screen, webtoons: $('mg-webtoons').checked ? '1' : '0' };
  }

  $('mg-save').onclick = async () => {
    try {
      await api.writeFile(CONFIG_PATH, btoa(JSON.stringify(currentConfig())));
      status('Saved. Read from the reader: Home → Plugins → Mangarr.');
    } catch (e) {
      status('Error: ' + e.message);
    }
  };

  $('mg-test').onclick = async () => {
    let cfg;
    try {
      cfg = currentConfig();
    } catch (e) {
      status('Error: ' + e.message);
      return;
    }
    status('Testing…');
    try {
      const r = await api.relay('GET', cfg.server + '/crosspoint/v1/chapters?list=continue&limit=1', { 'X-API-Key': cfg.key }, '');
      if (r.status >= 200 && r.status < 300) status('Connected to Mangarr.');
      else if (r.status === 401 || r.status === 403) status('Mangarr didn\'t accept the device key (HTTP ' + r.status + ').');
      else if (r.status === 404) status('That address isn\'t Mangarr\'s reading-app port (HTTP 404).');
      else status('Mangarr answered HTTP ' + (r.status || r.error) + '.');
    } catch (e) {
      status('Error: ' + e.message);
    }
  };

  const cfg = await loadConfig();
  if (cfg && cfg.server) {
    $('mg-server').value = cfg.server;
    $('mg-key').value = cfg.key || '';
    const preset = SCREENS.some(([v]) => v === cfg.screen && v !== 'custom');
    screenSel.value = preset ? cfg.screen : 'custom';
    customEl.value = preset ? '' : (cfg.screen || '');
    customEl.style.display = preset ? 'none' : '';
    $('mg-webtoons').checked = cfg.webtoons === '1';
    status('Configured. Read from the reader, or change the settings below.');
  } else {
    status('Not set up yet.');
  }
});
