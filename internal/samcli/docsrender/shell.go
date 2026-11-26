// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"html/template"
	"io"
)

type shellData struct {
	Title            string
	Description      string
	Body             template.HTML
	Nav              []*NavItem
	ActiveRoute      string
	IntroRoute       string
	SearchIndexRoute string
	LunrRoute        string
	MarkdownRoute    string
	LLMSFullRoute    string
	TOC              []TOCEntry
}

const mermaidCDN = "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs"

var shellTmpl = template.Must(template.New("shell").Funcs(template.FuncMap{
	"navTree": renderNavTree,
	"tocTree": renderTOC,
}).Parse(shellHTML))

func (r *Renderer) WritePage(w io.Writer, page *Page, nav []*NavItem, activeRoute string) error {
	return shellTmpl.Execute(w, shellData{
		Title:            page.Title,
		Description:      page.Description,
		Body:             template.HTML(page.BodyHTML), //nolint:gosec // rendered from trusted embedded docs
		Nav:              nav,
		ActiveRoute:      activeRoute,
		IntroRoute:       landingRoute(nav),
		SearchIndexRoute: searchIndexRoute,
		LunrRoute:        lunrRoute,
		MarkdownRoute:    activeRoute + ".md",
		LLMSFullRoute:    llmsFullRoute,
		TOC:              page.TOC,
	})
}

func renderTOC(toc []TOCEntry) template.HTML {
	if len(toc) < 2 {
		return ""
	}
	var b []byte
	b = append(b, `<nav class="toc" aria-label="On this page"><div class="toc-title">On this page</div><ul>`...)
	for _, e := range toc {
		b = append(b, `<li class="toc-l`...)
		b = append(b, byte('0'+e.Level))
		b = append(b, `"><a href="#`...)
		b = append(b, template.HTMLEscapeString(e.ID)...)
		b = append(b, `">`...)
		b = append(b, template.HTMLEscapeString(e.Text)...)
		b = append(b, "</a></li>"...)
	}
	b = append(b, "</ul></nav>"...)
	return template.HTML(b) //nolint:gosec // IDs/text escaped above
}

const caretSVG = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`

func renderNavTree(items []*NavItem, active string) template.HTML {
	if len(items) == 0 {
		return ""
	}
	var b []byte
	b = append(b, "<ul class=\"nav\">"...)
	for _, it := range items {
		if len(it.Children) > 0 {
			li := "<li class=\"nav-cat"
			if !navContainsActive(it, active) {
				li += " collapsed"
			}
			b = append(b, li...)
			b = append(b, `"><div class="nav-row">`...)
			b = append(b, navLabelHTML(it, active)...)
			b = append(b, `<button type="button" class="nav-caret" aria-label="Toggle section">`...)
			b = append(b, caretSVG...)
			b = append(b, "</button></div>"...)
			b = append(b, renderNavTree(it.Children, active)...)
			b = append(b, "</li>"...)
			continue
		}
		b = append(b, "<li>"...)
		b = append(b, navLabelHTML(it, active)...)
		b = append(b, "</li>"...)
	}
	b = append(b, "</ul>"...)
	return template.HTML(b) //nolint:gosec // labels/routes escaped above
}

func navLabelHTML(it *NavItem, active string) []byte {
	var b []byte
	if it.Route != "" {
		cls := "nav-link"
		if it.Route == active {
			cls += " active"
		}
		b = append(b, `<a class="`...)
		b = append(b, cls...)
		b = append(b, `" href="`...)
		b = append(b, template.HTMLEscapeString(it.Route)...)
		b = append(b, `">`...)
		b = append(b, template.HTMLEscapeString(it.Label)...)
		b = append(b, "</a>"...)
		return b
	}
	b = append(b, `<span class="nav-group">`...)
	b = append(b, template.HTMLEscapeString(it.Label)...)
	b = append(b, "</span>"...)
	return b
}

func navContainsActive(it *NavItem, active string) bool {
	if it.Route == active && active != "" {
		return true
	}
	for _, c := range it.Children {
		if navContainsActive(c, active) {
			return true
		}
	}
	return false
}

const shellHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Solace Agent Mesh</title>
{{if .Description}}<meta name="description" content="{{.Description}}">{{end}}
<style>
:root{--bg:#fff;--fg:#1c1e21;--muted:#606770;--border:#e3e6e8;--accent:#00c895;--code-bg:#f6f8fa;--sidebar:#f7f8fa}
*{box-sizing:border-box}
body{margin:0;font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:var(--fg);background:var(--bg)}
header{border-bottom:1px solid var(--border);position:sticky;top:0;background:var(--bg);z-index:10}
.header-inner{display:flex;align-items:center;gap:.6rem;padding:.7rem 1.2rem}
header a.brand{text-decoration:none;display:inline-flex;align-items:baseline;gap:.4rem;font-size:1.15rem;letter-spacing:-.01em}
header .brand-name{font-weight:800;color:var(--accent)}
header .brand-docs{font-weight:700;color:var(--fg);text-transform:uppercase;letter-spacing:.05em}
.view-raw{display:inline-flex;align-items:center;gap:.35rem;font-size:.85rem;color:var(--fg);text-decoration:none;padding:.35rem .65rem;border:1px solid var(--border);border-radius:6px;background:var(--bg)}
.view-raw:hover{border-color:var(--accent)}
.view-raw svg{width:15px;height:15px;opacity:.8}
.search{position:relative;margin-left:auto}
.search input{width:240px;padding:.4rem .6rem;border:1px solid var(--border);border-radius:6px;font-size:.9rem;background:var(--bg);color:var(--fg)}
.search input:focus{outline:none;border-color:var(--accent)}
.search-results{position:absolute;top:calc(100% + .3rem);right:0;width:360px;max-width:80vw;max-height:60vh;overflow:auto;background:var(--bg);border:1px solid var(--border);border-radius:6px;box-shadow:0 6px 24px rgba(0,0,0,.12);display:none;z-index:20}
.search-results a{display:block;padding:.5rem .7rem;color:var(--fg);text-decoration:none;border-bottom:1px solid var(--border);font-size:.9rem}
.search-results a:last-child{border-bottom:none}
.search-results a:hover,.search-results a.active{background:#eceff1}
.search-results .cat{color:var(--muted);font-size:.78rem}
.search-results .empty{padding:.6rem .7rem;color:var(--muted);font-size:.88rem}
.layout{display:flex;align-items:flex-start}
aside{flex:0 0 300px;position:sticky;top:56px;align-self:flex-start;max-height:calc(100vh - 56px);overflow:auto;padding:1rem;background:var(--sidebar);border-right:1px solid var(--border)}
main{flex:1;min-width:0;padding:1.5rem 2.5rem}
aside ul.nav{list-style:none;margin:0;padding-left:.6rem}
aside>ul.nav{padding-left:0}
aside li{margin:.15rem 0}
aside .nav-group{display:block;font-weight:600;color:var(--muted);margin-top:.6rem;font-size:.8rem;text-transform:uppercase;letter-spacing:.03em}
aside .nav-link{display:block;color:var(--fg);text-decoration:none;padding:.15rem .4rem;border-radius:4px;font-size:.92rem}
aside .nav-link:hover{background:#eceff1}
aside .nav-link.active{background:var(--accent);color:#fff}
aside li.nav-cat{margin-top:.5rem}
aside .nav-row{display:flex;align-items:center;gap:.3rem}
aside .nav-row>.nav-link,aside .nav-row>.nav-group{flex:1}
aside .nav-row .nav-group{margin-top:0}
aside .nav-caret{flex:none;display:inline-flex;align-items:center;justify-content:center;width:22px;height:22px;padding:0;border:none;background:none;color:var(--muted);cursor:pointer;border-radius:4px}
aside .nav-caret:hover{background:#eceff1;color:var(--fg)}
aside .nav-caret svg{width:14px;height:14px;transition:transform .15s ease}
aside li.nav-cat.collapsed>ul.nav{display:none}
aside li.nav-cat.collapsed>.nav-row .nav-caret svg{transform:rotate(-90deg)}
.toc{flex:0 0 230px;position:sticky;top:56px;align-self:flex-start;max-height:calc(100vh - 56px);overflow:auto;padding:1.5rem 1rem}
.toc .toc-title{font-weight:700;color:var(--fg);font-size:.78rem;text-transform:uppercase;letter-spacing:.04em;margin-bottom:.5rem}
.toc ul{list-style:none;margin:0;padding:0}
.toc li{margin:.1rem 0}
.toc a{display:block;color:var(--muted);text-decoration:none;font-size:.86rem;line-height:1.35;padding:.2rem .6rem;border-left:2px solid var(--border)}
.toc a:hover{color:var(--fg)}
.toc a.active{color:var(--accent);border-left-color:var(--accent);font-weight:600}
.toc-l3 a{padding-left:1.4rem}
@media(max-width:1100px){.toc{display:none}}
main :is(h1,h2,h3,h4){scroll-margin-top:70px}
main h1,main h2,main h3{line-height:1.25}
main a{color:#0b7285;text-decoration:none}
main a:hover{text-decoration:underline}
main img{max-width:100%}
main pre{background:var(--code-bg);padding:.9rem 1rem;border-radius:6px;overflow:auto}
main code{background:var(--code-bg);padding:.1rem .3rem;border-radius:4px;font-size:.9em}
main pre code{background:none;padding:0}
main table{border-collapse:collapse;width:100%;margin:1rem 0}
main th,main td{border:1px solid var(--border);padding:.5rem .7rem;text-align:left}
main blockquote{border-left:4px solid var(--border);margin:1rem 0;padding:.2rem 1rem;color:var(--muted)}
.admonition{border-radius:6px;padding:.2rem 1rem;margin:1rem 0;border-left:5px solid var(--muted);background:#f4f6f8}
.admonition-title{font-weight:700;text-transform:uppercase;font-size:.78rem;letter-spacing:.04em;margin:.6rem 0 .2rem}
.admonition-note{border-left-color:#5468ff;background:#eef0ff}
.admonition-tip{border-left-color:#00c895;background:#e7f9f2}
.admonition-info{border-left-color:#0b7285;background:#e6f4f7}
.admonition-warning,.admonition-caution{border-left-color:#e8a33d;background:#fff7e6}
.admonition-danger{border-left-color:#e5484d;background:#ffeef0}
.mermaid{margin:1rem 0;text-align:center}
.page-actions-bar{display:flex;justify-content:flex-end;margin:0 0 1rem}
.page-actions{position:relative;display:inline-flex}
.page-actions .pa-main,.page-actions .pa-toggle{font:inherit;font-size:.85rem;cursor:pointer;background:var(--bg);color:var(--fg);border:1px solid var(--border)}
.page-actions .pa-main{display:inline-flex;align-items:center;gap:.4rem;padding:.4rem .75rem;border-radius:6px 0 0 6px;border-right:none}
.page-actions .pa-toggle{display:inline-flex;align-items:center;padding:.4rem .45rem;border-radius:0 6px 6px 0}
.page-actions .pa-main:hover,.page-actions .pa-toggle:hover,.page-actions .pa-toggle[aria-expanded=true]{background:var(--sidebar)}
.page-actions .pa-main.copied{border-color:var(--accent);background:var(--accent);color:#fff}
.page-actions svg{width:15px;height:15px;flex:none;opacity:.85}
.pa-menu{position:absolute;top:calc(100% + .35rem);right:0;min-width:230px;background:var(--bg);border:1px solid var(--border);border-radius:8px;box-shadow:0 8px 28px rgba(0,0,0,.14);padding:.3rem;z-index:30}
.pa-menu[hidden]{display:none}
.pa-menu>*{display:flex;align-items:center;gap:.65rem;width:100%;text-align:left;padding:.55rem .6rem;border:none;background:none;border-radius:6px;color:var(--fg);text-decoration:none;font:inherit;font-size:.92rem;cursor:pointer}
.pa-menu>*:hover{background:var(--sidebar)}
</style>
</head>
<body>
<header>
<div class="header-inner">
<a class="brand" href="{{.IntroRoute}}"><span class="brand-name">Solace Agent Mesh</span><span class="brand-docs">Docs</span></a>
<div class="search">
<input id="doc-search" type="search" placeholder="Search docs…" autocomplete="off" spellcheck="false" aria-label="Search documentation">
<div id="doc-search-results" class="search-results"></div>
</div>
<a class="view-raw" href="{{.LLMSFullRoute}}" target="_blank" rel="noopener" title="View all documentation as a single raw Markdown file — copy its text into a cloud AI chat, or its URL into a local AI tool"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/></svg>Full docs (Markdown)</a>
</div>
</header>
<div class="layout">
<aside>{{navTree .Nav .ActiveRoute}}</aside>
<main>
<div class="page-actions-bar">
<div class="page-actions">
<button type="button" class="pa-main" id="copy-page" data-url="{{.MarkdownRoute}}" title="Copy this page as Markdown"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg><span class="label">Copy page</span></button>
<button type="button" class="pa-toggle" id="pa-toggle" aria-haspopup="menu" aria-expanded="false" aria-label="More page actions"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg></button>
<div class="pa-menu" id="pa-menu" role="menu" hidden>
<button type="button" role="menuitem" id="copy-page-item" data-url="{{.MarkdownRoute}}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg><span class="label">Copy page as Markdown</span></button>
<a role="menuitem" id="open-md" href="{{.MarkdownRoute}}" target="_blank" rel="noopener"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 17 17 7"/><path d="M7 7h10v10"/></svg>Open Markdown</a>
</div>
</div>
</div>
{{.Body}}</main>
{{tocTree .TOC}}
</div>
<script>
(function(){
  // localhost / 127.0.0.1 is a secure context, so navigator.clipboard works
  // over plain HTTP here; execCommand is the fallback for older engines.
  function fallbackCopy(text){
    var ta=document.createElement("textarea");
    ta.value=text;ta.style.position="fixed";ta.style.opacity="0";
    document.body.appendChild(ta);ta.focus();ta.select();
    try{document.execCommand("copy");}catch(e){}
    document.body.removeChild(ta);
  }
  // Flash feedback on the main split button (always), so a copy from the menu
  // item still confirms on the visible primary control.
  function flash(){
    var main=document.getElementById("copy-page"),label=main&&main.querySelector(".label");
    if(!label)return;
    var prev=label.textContent;
    label.textContent="Copied!";main.classList.add("copied");
    setTimeout(function(){label.textContent=prev;main.classList.remove("copied");},1500);
  }
  function copyText(text){
    if(navigator.clipboard&&navigator.clipboard.writeText){
      navigator.clipboard.writeText(text).then(flash,function(){fallbackCopy(text);flash();});
    }else{fallbackCopy(text);flash();}
  }
  function copyPage(url){
    fetch(url).then(function(r){return r.text();}).then(copyText).catch(function(){});
  }
  var menu=document.getElementById("pa-menu"),toggle=document.getElementById("pa-toggle");
  function closeMenu(){if(menu){menu.hidden=true;toggle.setAttribute("aria-expanded","false");}}
  var main=document.getElementById("copy-page");
  if(main)main.addEventListener("click",function(){copyPage(main.dataset.url);});
  var item=document.getElementById("copy-page-item");
  if(item)item.addEventListener("click",function(){copyPage(item.dataset.url);closeMenu();});
  if(toggle&&menu){
    toggle.addEventListener("click",function(e){
      e.stopPropagation();
      var open=menu.hidden;menu.hidden=!open;toggle.setAttribute("aria-expanded",String(open));
    });
    document.addEventListener("click",function(e){if(!e.target.closest(".page-actions"))closeMenu();});
    document.addEventListener("keydown",function(e){if(e.key==="Escape")closeMenu();});
  }
})();
(function(){
  var box=document.getElementById("doc-search");
  var panel=document.getElementById("doc-search-results");
  if(!box||!panel)return;
  var idx=null,docs=null,pending=null,active=-1;
  function loadLunr(){
    return new Promise(function(res,rej){
      if(window.lunr)return res();
      var s=document.createElement("script");
      s.src="{{.LunrRoute}}";s.onload=function(){res();};s.onerror=rej;
      document.head.appendChild(s);
    });
  }
  function build(){
    if(idx)return Promise.resolve();
    if(!pending){
      pending=Promise.all([loadLunr(),fetch("{{.SearchIndexRoute}}").then(function(r){return r.json();})]).then(function(out){
        docs=out[1];
        idx=lunr(function(){
          this.ref("route");this.field("title",{boost:10});this.field("text");
          var self=this;docs.forEach(function(d){self.add(d);});
        });
      });
    }
    return pending;
  }
  function category(route){
    var p=route.split("/").filter(Boolean);
    return p.length<2?"":p[p.length-2].replace(/-/g," ");
  }
  function lookup(route){for(var i=0;i<docs.length;i++)if(docs[i].route===route)return docs[i];}
  function search(q){
    return idx.query(function(qb){
      q.toLowerCase().split(/\s+/).filter(Boolean).forEach(function(t){
        qb.term(t,{boost:10});
        qb.term(t,{wildcard:lunr.Query.wildcard.TRAILING});
        qb.term(t,{editDistance:1});
      });
    });
  }
  function render(q){
    active=-1;
    if(!idx||!q){panel.style.display="none";panel.innerHTML="";return;}
    var hits=search(q).slice(0,12);
    if(!hits.length){panel.innerHTML='<div class="empty">No results</div>';panel.style.display="block";return;}
    panel.innerHTML=hits.map(function(h){
      var d=lookup(h.ref),cat=category(d.route);
      return '<a href="'+d.route+'">'+(cat?'<span class="cat">'+cat+" › </span>":"")+d.title+"</a>";
    }).join("");
    panel.style.display="block";
  }
  function items(){return panel.querySelectorAll("a");}
  function highlight(){var a=items();for(var i=0;i<a.length;i++)a[i].classList.toggle("active",i===active);}
  box.addEventListener("focus",build,{once:true});
  box.addEventListener("input",function(){var q=box.value.trim();build().then(function(){render(q);});});
  box.addEventListener("keydown",function(e){
    var a=items();if(!a.length)return;
    if(e.key==="ArrowDown"){e.preventDefault();active=(active+1)%a.length;highlight();}
    else if(e.key==="ArrowUp"){e.preventDefault();active=(active-1+a.length)%a.length;highlight();}
    else if(e.key==="Enter"&&active>=0){e.preventDefault();window.location.href=a[active].getAttribute("href");}
    else if(e.key==="Escape"){panel.style.display="none";box.blur();}
  });
  document.addEventListener("click",function(e){if(!e.target.closest(".search"))panel.style.display="none";});
})();
(function(){
  // Collapsible left-sidebar categories: clicking the caret toggles its section.
  document.querySelectorAll("aside .nav-caret").forEach(function(btn){
    btn.addEventListener("click",function(e){
      e.preventDefault();e.stopPropagation();
      var li=btn.closest("li.nav-cat");if(li)li.classList.toggle("collapsed");
    });
  });
})();
(function(){
  // Right-side TOC scrollspy: highlight the link for the heading nearest the top.
  var toc=document.querySelector(".toc");if(!toc)return;
  var links=Array.prototype.slice.call(toc.querySelectorAll("a"));
  if(!links.length)return;
  var byId={};links.forEach(function(a){byId[a.getAttribute("href").slice(1)]=a;});
  var heads=links.map(function(a){return document.getElementById(a.getAttribute("href").slice(1));}).filter(Boolean);
  function setActive(id){links.forEach(function(a){a.classList.toggle("active",a.getAttribute("href").slice(1)===id);});}
  var obs=new IntersectionObserver(function(entries){
    entries.forEach(function(en){if(en.isIntersecting)setActive(en.target.id);});
  },{rootMargin:"-70px 0px -70% 0px"});
  heads.forEach(function(h){obs.observe(h);});
})();
</script>
<script type="module">
import mermaid from "` + mermaidCDN + `";
mermaid.initialize({startOnLoad:true});
</script>
</body>
</html>`
