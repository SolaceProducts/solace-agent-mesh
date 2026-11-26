// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	wailslogger "github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/SolaceProducts/solace-agent-mesh/internal/desktop/frontend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type RunOption func(*App)

func WithExtensionProvider(ep embedded.ExtensionProvider) RunOption {
	return func(a *App) {
		a.enterprise = ep
	}
}

func WithConfigDirs(dirs ...string) RunOption {
	return func(a *App) {
		a.configDirs = dirs
	}
}

func WithDefaultConfigs(configs map[string][]byte) RunOption {
	return func(a *App) {
		a.defaultConfigs = configs
	}
}

func WithAdditionalConfigs(configs map[string][]byte) RunOption {
	return func(a *App) {
		a.additionalConfigs = configs
	}
}

func WithStartURL(url string) RunOption {
	return func(a *App) {
		a.startURL = url
	}
}

func WithShowConsole() RunOption {
	return func(a *App) {
		a.showConsole = true
	}
}

//go:embed appicon.png
var appIcon []byte

func Run(opts ...RunOption) error {
	app := NewApp()
	for _, opt := range opts {
		opt(app)
	}

	app.startDiagnostics()

	mainAssets, err := frontend.Assets()
	if err != nil {
		return fmt.Errorf("load frontend assets: %w", err)
	}

	startURLApplied := false
	var shuttingDown, cleanupDone atomic.Bool
	onDomReady := func(ctx context.Context) {
		if app.startURL != "" && !startURLApplied {
			startURLApplied = true
			escaped, _ := json.Marshal(app.startURL)
			wailsruntime.WindowExecJS(ctx, `window.location.hash = `+string(escaped)+`;`)
		}
		wailsruntime.WindowExecJS(ctx, startupProgressJS)

		wailsruntime.WindowExecJS(ctx, downloadInterceptJS)

		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			wailsruntime.WindowExecJS(ctx, fileInputInterceptJS)
		}

		wailsruntime.WindowExecJS(ctx, windowOpenInterceptJS)

		wailsruntime.WindowExecJS(ctx, anchorInterceptJS)

		if app.showConsole {
			wailsruntime.WindowExecJS(ctx, consoleOverlayJS)
		}

	}

	featureFlags := features.NewRegistry()
	if err := features.LoadFlags(featureFlags); err != nil {
		app.logger.Warn("feature flag catalog failed to load", "error", err)
	}

	return wails.Run(&options.App{
		Title:     windowTitle(runtime.GOOS, featureFlags),
		Width:     1280,
		Height:    900,
		MinWidth:  800,
		MinHeight: 600,
		Debug: options.Debug{
			OpenInspectorOnStartup: false,
		},
		Logger:             newWailsLogger(app.logger),
		LogLevel:           wailslogger.INFO,
		LogLevelProduction: wailslogger.INFO,
		BackgroundColour:   &options.RGBA{R: 233, G: 236, B: 240, A: 255},
		AssetServer: &assetserver.Options{
			Assets: mainAssets,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: samversion.AppIdentifier,
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				wailsruntime.WindowUnminimise(app.ctx)
				wailsruntime.Show(app.ctx)
				wailsruntime.WindowSetAlwaysOnTop(app.ctx, true)
				wailsruntime.WindowSetAlwaysOnTop(app.ctx, false)
			},
		},
		OnStartup:  app.Startup,
		OnDomReady: onDomReady,
		OnBeforeClose: func(ctx context.Context) bool {
			if cleanupDone.Load() {
				return false
			}
			if shuttingDown.Swap(true) {
				return true
			}
			wailsruntime.WindowExecJS(ctx, goodbyeOverlayJS)
			go func() {
				app.Shutdown(ctx)
				cleanupDone.Store(true)
				wailsruntime.Quit(ctx)
			}()
			return true
		},
		OnShutdown: app.Shutdown,
		Bind: []any{
			app,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Solace Agent Mesh",
				Message: "Version " + samversion.Version,
				Icon:    appIcon,
			},
		},
		Linux: &linux.Options{
			Icon: appIcon,
		},
	})
}

const downloadInterceptJS = `
(function() {
  if (window.__samDownloadInterceptInstalled) return;
  window.__samDownloadInterceptInstalled = true;

  var origClick = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function() {
    var dl = this.download || this.getAttribute('download');
    if (dl === null || dl === undefined) {
      return origClick.call(this);
    }

    var href = this.href || this.getAttribute('href');
    if (!href) {
      return origClick.call(this);
    }

    var filename = dl || 'download';

    try {
      var xhr = new XMLHttpRequest();
      xhr.open('GET', href, false);
      xhr.responseType = 'arraybuffer';
      xhr.send();

      if (xhr.status === 200 || xhr.status === 0) {
        var bytes = new Uint8Array(xhr.response);
        var binary = '';
        var chunkSize = 8192;
        for (var i = 0; i < bytes.length; i += chunkSize) {
          var chunk = bytes.subarray(i, Math.min(i + chunkSize, bytes.length));
          binary += String.fromCharCode.apply(null, chunk);
        }
        var b64 = btoa(binary);
        setTimeout(function() {
          window.go.desktop.App.SaveFile(b64, filename).catch(function(err) {
            console.error('[SAM-DL] SaveFile error:', err);
            try {
              window.dispatchEvent(new CustomEvent('desktop:fileDialogError', {
                detail: { op: 'save', message: String(err) }
              }));
            } catch (_) {}
          });
        }, 100);
      } else {
        origClick.call(this);
      }
    } catch(e) {
      console.error('[SAM-DL] intercept error:', e);
      origClick.call(this);
    }
  };
})();
`

const windowOpenInterceptJS = `
(function() {
  if (window.__samWindowOpenInstalled) return;
  window.__samWindowOpenInstalled = true;

  var stub = {
    focus: function(){},
    blur: function(){},
    close: function(){},
    closed: false,
    postMessage: function(){},
    location: { href: '' }
  };

  function openExternal(url) {
    if (window.runtime && typeof window.runtime.BrowserOpenURL === 'function') {
      try {
        window.runtime.BrowserOpenURL(url);
      } catch (e) {
        console.error('[SAM] BrowserOpenURL failed', e);
      }
    }
  }

  window.open = function(url) {
    if (!url) return stub;
    var s = String(url);

    // Hash route ("#/x" or "/#/x") → client-side navigation, no reload.
    if (s.charAt(0) === '#' || s.indexOf('/#') === 0) {
      window.location.hash = s.slice(s.indexOf('#'));
      return stub;
    }

    // Any other same-origin URL is an in-app navigation; a different origin
    // is genuinely external and goes to the system browser.
    try {
      var resolved = new URL(s, window.location.href);
      if (resolved.origin === window.location.origin) {
        window.location.href = resolved.href;
      } else {
        openExternal(s);
      }
    } catch (e) {
      // Not a parseable URL — treat as external (best effort).
      openExternal(s);
    }
    return stub;
  };
})();
`

const anchorInterceptJS = `
(function() {
  if (window.__samAnchorInterceptInstalled) return;
  window.__samAnchorInterceptInstalled = true;

  function openExternal(url) {
    if (window.runtime && typeof window.runtime.BrowserOpenURL === 'function') {
      try {
        window.runtime.BrowserOpenURL(url);
      } catch (e) {
        console.error('[SAM] BrowserOpenURL failed', e);
      }
    }
  }

  document.addEventListener('click', function(e) {
    if (e.defaultPrevented || e.button !== 0) return;
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;

    var el = e.target;
    while (el && el.nodeName !== 'A') el = el.parentNode;
    if (!el || el.nodeName !== 'A' || el.hasAttribute('download')) return;
    if (!el.getAttribute('href')) return;

    var resolved;
    try {
      resolved = new URL(el.href, window.location.href);
    } catch (err) {
      return;
    }

    if (resolved.protocol !== 'http:' && resolved.protocol !== 'https:') return;
    if (resolved.origin === window.location.origin) return;

    e.preventDefault();
    openExternal(resolved.href);
  }, true);
})();
`

const fileInputInterceptJS = `
(function() {
  if (window.__samFileInputInterceptInstalled) return;
  window.__samFileInputInterceptInstalled = true;

  function patchInput(input) {
    if (input.__samPatched) return;
    input.__samPatched = true;
    // Replace .click() as an own property — shadows HTMLElement.prototype.click
    // so the native WebKit file dialog is never invoked.
    input.click = function() {
      handleFileInput(input);
    };
  }

  function handleFileInput(input) {
    var accept = input.accept || '';

    window.go.desktop.App.PickFile(accept).then(function(path) {
      if (!path) return;

      window.go.desktop.App.ReadFileBase64(path).then(function(b64) {
        var binary = atob(b64);
        var bytes = new Uint8Array(binary.length);
        for (var i = 0; i < binary.length; i++) {
          bytes[i] = binary.charCodeAt(i);
        }

        var filename = path.split('/').pop() || 'file';
        var ext = filename.split('.').pop().toLowerCase();
        var mimeMap = {
          'png': 'image/png', 'jpg': 'image/jpeg', 'jpeg': 'image/jpeg',
          'gif': 'image/gif', 'webp': 'image/webp', 'svg': 'image/svg+xml',
          'pdf': 'application/pdf', 'txt': 'text/plain', 'csv': 'text/csv',
          'json': 'application/json', 'xml': 'application/xml',
          'zip': 'application/zip', 'mp4': 'video/mp4', 'mp3': 'audio/mpeg',
          'wav': 'audio/wav', 'doc': 'application/msword',
          'docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          'xls': 'application/vnd.ms-excel',
          'xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
          'pptx': 'application/vnd.openxmlformats-officedocument.presentationml.presentation'
        };
        var mime = mimeMap[ext] || 'application/octet-stream';

        var file = new File([bytes], filename, { type: mime });
        var dt = new DataTransfer();
        dt.items.add(file);
        input.files = dt.files;

        input.dispatchEvent(new Event('change', { bubbles: true }));
      }).catch(function(err) {
        console.error('[SAM] ReadFileBase64 error:', err);
        emitFileDialogError('open', err);
      });
    }).catch(function(err) {
      console.error('[SAM] PickFile error:', err);
      emitFileDialogError('open', err);
    });
  }

  function emitFileDialogError(op, err) {
    try {
      window.dispatchEvent(new CustomEvent('desktop:fileDialogError', {
        detail: { op: op, message: String(err) }
      }));
    } catch (_) {}
  }

  // Patch all existing file inputs.
  document.querySelectorAll('input[type="file"]').forEach(patchInput);

  // Watch for dynamically added file inputs (React re-creates elements).
  var observer = new MutationObserver(function(mutations) {
    for (var i = 0; i < mutations.length; i++) {
      var added = mutations[i].addedNodes;
      for (var j = 0; j < added.length; j++) {
        var node = added[j];
        if (node.nodeType !== 1) continue;
        if (node.tagName === 'INPUT' && node.type === 'file') {
          patchInput(node);
        } else if (node.querySelectorAll) {
          node.querySelectorAll('input[type="file"]').forEach(patchInput);
        }
      }
    }
  });
  observer.observe(document.body, { childList: true, subtree: true });
})();
`

const startupProgressJS = `
(function() {
  if (window.__samProgressInstalled) return;
  window.__samProgressInstalled = true;

  // Find the loading text node (created by the frontend's loading screen).
  function findLoadingEl() {
    if (window.__samProgressEl) return window.__samProgressEl;
    var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, null, false);
    while (walker.nextNode()) {
      var node = walker.currentNode;
      if (node.textContent && node.textContent.trim().match(/^(Loading|Discovering|Starting|Preparing|Building|Pulling|Installing|Downloading|Copying|Finalizing|Launching|Ready|Sandbox|STR|Python)/)) {
        window.__samProgressEl = node;
        return node;
      }
    }
    return null;
  }

  // Lazily create (or reuse) a detail element below the main loading text.
  function getDetailEl(parentEl) {
    if (window.__samProgressDetailEl) return window.__samProgressDetailEl;
    var detail = document.createElement('div');
    detail.style.fontSize = '0.85em';
    detail.style.opacity = '0.7';
    detail.style.marginTop = '4px';
    // Insert after the parent element's container.
    var container = parentEl.parentNode;
    if (container) {
      container.appendChild(detail);
    }
    window.__samProgressDetailEl = detail;
    return detail;
  }

  function updateProgress(msg) {
    var el = findLoadingEl();
    if (!el) return;

    var parts = msg.split('\n');
    var header = parts[0];
    var detail = parts.length > 1 ? parts[1] : '';

    el.textContent = header;

    if (detail) {
      var detailEl = getDetailEl(el);
      detailEl.textContent = detail;
      detailEl.style.display = '';
    } else if (window.__samProgressDetailEl) {
      // Single-line message: hide the detail row.
      window.__samProgressDetailEl.style.display = 'none';
    }
  }

  // Listen for future progress events.
  window.runtime.EventsOn('startup:progress', updateProgress);

  // Fetch the current progress (events before DOM ready are lost).
  window.go.desktop.App.StartupProgress().then(function(msg) {
    if (msg) updateProgress(msg);
  });
})();
`

const goodbyeOverlayJS = `
(function() {
  if (document.getElementById('sam-goodbye-overlay')) return;

  var overlay = document.createElement('div');
  overlay.id = 'sam-goodbye-overlay';
  overlay.style.cssText = 'position:fixed;top:0;left:0;width:100%;height:100%;' +
    'display:flex;flex-direction:column;align-items:center;justify-content:center;' +
    'background:rgba(0,0,0,0.85);z-index:999999;color:white;font-family:-apple-system,BlinkMacSystemFont,sans-serif;' +
    'animation:samFadeIn 0.3s ease-out;';

  overlay.innerHTML = '<div style="font-size:2em;margin-bottom:16px;">See you later!</div>' +
    '<div style="display:flex;align-items:center;gap:8px;font-size:1em;opacity:0.6;">' +
      '<div class="sam-spinner"></div>Shutting down\u2026' +
    '</div>';

  var style = document.createElement('style');
  style.textContent = '@keyframes samFadeIn{from{opacity:0}to{opacity:1}}' +
    '@keyframes samSpin{to{transform:rotate(360deg)}}' +
    '.sam-spinner{width:16px;height:16px;border:2px solid rgba(255,255,255,0.3);' +
    'border-top-color:white;border-radius:50%;animation:samSpin 0.8s linear infinite;}';
  document.head.appendChild(style);
  document.body.appendChild(overlay);
})();
`

const consoleOverlayJS = `
(function() {
  if (window.__samConsoleInstalled) return;
  window.__samConsoleInstalled = true;

  var MAX_ENTRIES = 500;
  var entries = [];
  var expanded = false;
  var unread = 0;

  // Create root container
  var root = document.createElement('div');
  root.id = 'sam-console';
  root.style.cssText = 'position:fixed;bottom:8px;right:8px;z-index:99999;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:11px;';

  // Pill (collapsed state)
  var pill = document.createElement('div');
  pill.style.cssText = 'cursor:pointer;background:rgba(0,0,0,0.8);color:#aaa;padding:6px 14px;border-radius:16px;user-select:none;display:flex;align-items:center;gap:6px;backdrop-filter:blur(8px);border:1px solid rgba(255,255,255,0.1);';
  pill.innerHTML = '<span style="font-size:13px;">&#9002;</span> Console <span id="sam-console-badge" style="display:none;background:#e55;color:#fff;border-radius:10px;padding:0 6px;font-size:10px;min-width:16px;text-align:center;"></span>';
  var badge = pill.querySelector('#sam-console-badge');

  // Panel (expanded state)
  var panel = document.createElement('div');
  panel.style.cssText = 'display:none;width:480px;height:320px;background:rgba(10,10,10,0.92);border:1px solid rgba(255,255,255,0.15);border-radius:8px;backdrop-filter:blur(12px);flex-direction:column;overflow:hidden;resize:both;min-width:320px;min-height:200px;max-width:80vw;max-height:80vh;';

  // Panel header
  var header = document.createElement('div');
  header.style.cssText = 'display:flex;align-items:center;justify-content:space-between;padding:6px 10px;border-bottom:1px solid rgba(255,255,255,0.1);color:#aaa;flex-shrink:0;cursor:default;';
  header.innerHTML = '<span>Console</span><div id="sam-console-actions" style="display:flex;gap:8px;"></div>';

  var actions = header.querySelector('#sam-console-actions');

  var clearBtn = document.createElement('span');
  clearBtn.textContent = 'Clear';
  clearBtn.style.cssText = 'cursor:pointer;color:#888;font-size:10px;padding:2px 6px;border-radius:4px;border:1px solid rgba(255,255,255,0.1);';
  clearBtn.onclick = function() { entries = []; renderEntries(); };

  var closeBtn = document.createElement('span');
  closeBtn.textContent = '\u2715';
  closeBtn.style.cssText = 'cursor:pointer;color:#888;font-size:12px;padding:2px 6px;';
  closeBtn.onclick = function() { toggle(); };

  actions.appendChild(clearBtn);
  actions.appendChild(closeBtn);

  // Log area
  var logArea = document.createElement('div');
  logArea.style.cssText = 'flex:1;overflow-y:auto;padding:4px 8px;color:#ccc;white-space:pre-wrap;word-break:break-all;';

  // Filter buttons
  var filterBar = document.createElement('div');
  filterBar.style.cssText = 'display:flex;gap:2px;padding:4px 8px;border-bottom:1px solid rgba(255,255,255,0.06);flex-shrink:0;';

  var activeFilter = 'all';
  var LEVELS = [
    { key: 'all', label: 'All', color: '#aaa' },
    { key: 'log', label: 'Log', color: '#8f8' },
    { key: 'warn', label: 'Warn', color: '#ff8' },
    { key: 'error', label: 'Error', color: '#f88' }
  ];
  var filterBtns = {};
  LEVELS.forEach(function(lvl) {
    var btn = document.createElement('span');
    btn.textContent = lvl.label;
    btn.dataset.level = lvl.key;
    btn.style.cssText = 'cursor:pointer;padding:2px 8px;border-radius:4px;font-size:10px;color:' + lvl.color + ';';
    btn.onclick = function() {
      activeFilter = lvl.key;
      updateFilterHighlight();
      renderEntries();
    };
    filterBtns[lvl.key] = btn;
    filterBar.appendChild(btn);
  });

  function updateFilterHighlight() {
    Object.keys(filterBtns).forEach(function(k) {
      filterBtns[k].style.background = k === activeFilter ? 'rgba(255,255,255,0.1)' : 'transparent';
    });
  }
  updateFilterHighlight();

  panel.appendChild(header);
  panel.appendChild(filterBar);
  panel.appendChild(logArea);
  root.appendChild(pill);
  root.appendChild(panel);
  document.body.appendChild(root);

  var LEVEL_COLORS = { log: '#8f8', warn: '#ff8', error: '#f88', info: '#8af' };

  function renderEntries() {
    var filtered = activeFilter === 'all' ? entries : entries.filter(function(e) { return e.level === activeFilter || (activeFilter === 'log' && e.level === 'info'); });
    logArea.innerHTML = '';
    filtered.forEach(function(e) {
      var line = document.createElement('div');
      line.style.cssText = 'padding:2px 0;border-bottom:1px solid rgba(255,255,255,0.04);color:' + (LEVEL_COLORS[e.level] || '#ccc') + ';';
      var ts = document.createElement('span');
      ts.style.cssText = 'color:#666;margin-right:6px;';
      ts.textContent = e.time;
      line.appendChild(ts);
      line.appendChild(document.createTextNode(e.text));
      logArea.appendChild(line);
    });
    logArea.scrollTop = logArea.scrollHeight;
  }

  function addEntry(level, args) {
    var text = Array.prototype.slice.call(args).map(function(a) {
      if (typeof a === 'string') return a;
      try { return JSON.stringify(a, null, 2); } catch(e) { return String(a); }
    }).join(' ');
    var now = new Date();
    var time = ('0'+now.getHours()).slice(-2)+':'+('0'+now.getMinutes()).slice(-2)+':'+('0'+now.getSeconds()).slice(-2)+'.'+('00'+now.getMilliseconds()).slice(-3);
    entries.push({ level: level, text: text, time: time });
    if (entries.length > MAX_ENTRIES) entries.shift();
    if (expanded) {
      renderEntries();
    } else {
      unread++;
      badge.textContent = unread > 99 ? '99+' : String(unread);
      badge.style.display = 'inline-block';
    }
  }

  function toggle() {
    expanded = !expanded;
    pill.style.display = expanded ? 'none' : 'flex';
    panel.style.display = expanded ? 'flex' : 'none';
    if (expanded) {
      unread = 0;
      badge.style.display = 'none';
      renderEntries();
    }
  }

  pill.onclick = toggle;

  // Keyboard shortcut: Ctrl/Cmd+Shift+J
  document.addEventListener('keydown', function(e) {
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'J') {
      e.preventDefault();
      toggle();
    }
  });

  // Monkey-patch console methods
  var origLog = console.log;
  var origWarn = console.warn;
  var origError = console.error;
  var origInfo = console.info;

  console.log = function() { addEntry('log', arguments); origLog.apply(console, arguments); };
  console.warn = function() { addEntry('warn', arguments); origWarn.apply(console, arguments); };
  console.error = function() { addEntry('error', arguments); origError.apply(console, arguments); };
  console.info = function() { addEntry('info', arguments); origInfo.apply(console, arguments); };

  // Also capture unhandled errors
  window.addEventListener('error', function(e) {
    addEntry('error', ['Uncaught: ' + (e.message || e) + (e.filename ? ' (' + e.filename + ':' + e.lineno + ')' : '')]);
  });
  window.addEventListener('unhandledrejection', function(e) {
    addEntry('error', ['Unhandled rejection: ' + (e.reason && e.reason.message ? e.reason.message : String(e.reason))]);
  });
})();
`
