/**
 * TENOM — Automated Network Reliability Monitor
 * Frontend Application Engine (Vanilla JS + Apache ECharts 6.1.0)
 */

(function () {
  'use strict';

  // --- Configuration & Global State ---
  const CONFIG = {
    pollIntervalMs: 5000,          // Live polling for /status every 5s
    chartRefreshIntervalMs: 25000, // Metrics auto-refresh interval
    endpoints: {
      status: '/api/v1/status',
      pingMetrics: '/api/v1/metrics/latency',
      dnsMetrics: '/api/v1/metrics/dns',
      incidents: '/api/v1/incidents',
      config: '/api/v1/config'
    },
    colors: {
      cyan: '#06b6d4',
      emerald: '#10b981',
      purple: '#8b5cf6',
      amber: '#f59e0b',
      rose: '#f43f5e',
      blue: '#3b82f6',
      zincBorder: '#27272a',
      zincTextMuted: '#71717a',
      zincDark: '#18181b'
    }
  };

  const state = {
    statusData: null,
    pingRange: '1h',
    pingMetric: 'rtt',        // 'rtt' | 'loss' | 'both'
    pingSelectedTarget: 'all', // 'all' or specific target host
    dnsRange: '1h',
    dnsMode: 'timeline',      // 'timeline' | 'bar'
    incidents: [],
    outageActive: false,
    outageDurationSeconds: 0,
    outageTimerId: null,
    pingChart: null,
    dnsChart: null,
    activeTracerouteText: '',
    knownTargets: [],
    cachedConfig: null,
    workingConfig: null
  };

  // High-contrast NOC neon palette for multi-target series
  const SERIES_PALETTE = [
    '#06b6d4', // Cyan (LAN / Gateway)
    '#10b981', // Emerald (Cloudflare)
    '#8b5cf6', // Violet (Google DNS)
    '#f59e0b', // Amber (Secondary)
    '#3b82f6', // Blue
    '#ec4899', // Pink
    '#14b8a6', // Teal
    '#f97316', // Orange
    '#a855f7', // Purple
    '#6366f1'  // Indigo
  ];

  // --- DOM Elements Cache ---
  const DOM = {
    // Header
    systemStatusBadge: document.getElementById('system-status-badge'),
    systemStatusText: document.getElementById('system-status-text'),
    lastUpdatedText: document.getElementById('last-updated-text'),
    btnRefresh: document.getElementById('btn-refresh'),
    btnSettings: document.getElementById('btn-settings'),

    // Outage Banner
    outageBanner: document.getElementById('outage-alert-banner'),
    outageDurationCounter: document.getElementById('outage-duration-counter'),

    // KPI Cards
    kpiStateVal: document.getElementById('kpi-state-val'),
    kpiStateDesc: document.getElementById('kpi-state-desc'),
    kpiStateBar: document.getElementById('kpi-state-bar'),
    kpiUptimeVal: document.getElementById('kpi-uptime-val'),
    slaProgressBar: document.getElementById('sla-progress-bar'),
    kpiGatewayVal: document.getElementById('kpi-gateway-val'),
    kpiGatewayLoss: document.getElementById('kpi-gateway-loss'),
    kpiGatewayHost: document.getElementById('kpi-gateway-host'),
    kpiWanVal: document.getElementById('kpi-wan-val'),
    kpiWanLoss: document.getElementById('kpi-wan-loss'),
    kpiWanHost: document.getElementById('kpi-wan-host'),
    kpiDnsVal: document.getElementById('kpi-dns-val'),
    kpiDnsFastest: document.getElementById('kpi-dns-fastest'),

    // Targets Strip
    targetsContainer: document.getElementById('targets-container'),
    targetsCount: document.getElementById('targets-count'),

    // Ping Chart
    chartPingEl: document.getElementById('chart-ping'),
    chartPingEmpty: document.getElementById('chart-ping-empty'),
    pingRangePicker: document.getElementById('ping-range-picker'),
    pingTargetSelect: document.getElementById('ping-target-select'),

    // DNS Chart
    chartDnsEl: document.getElementById('chart-dns'),
    chartDnsEmpty: document.getElementById('chart-dns-empty'),
    dnsRangePicker: document.getElementById('dns-range-picker'),
    dnsViewToggle: document.getElementById('dns-view-toggle'),

    // Incidents Table
    tableIncidents: document.getElementById('table-incidents'),
    incidentsTbody: document.getElementById('incidents-tbody'),
    incidentsEmptyState: document.getElementById('incidents-empty-state'),
    incidentBadgeCount: document.getElementById('incident-badge-count'),
    incidentFilterInput: document.getElementById('incident-filter-input'),

    // Traceroute Modal
    tracerouteModal: document.getElementById('traceroute-modal'),
    btnModalClose: document.getElementById('btn-modal-close'),
    btnCopyTraceroute: document.getElementById('btn-copy-traceroute'),
    copyBtnText: document.getElementById('copy-btn-text'),
    modalCommand: document.getElementById('modal-traceroute-command'),
    modalIncidentId: document.getElementById('modal-incident-id'),
    modalIncidentReason: document.getElementById('modal-incident-reason'),
    modalIncidentTime: document.getElementById('modal-incident-time'),
    modalTracerouteCode: document.getElementById('modal-traceroute-code'),

    // Settings Drawer
    settingsDrawer: document.getElementById('settings-drawer'),
    settingsBackdrop: document.getElementById('settings-drawer-backdrop'),
    btnCloseDrawer: document.getElementById('btn-close-drawer'),
    btnSaveConfig: document.getElementById('btn-save-config'),
    btnDiscardConfig: document.getElementById('btn-discard-config'),
    configTargetsTbody: document.getElementById('config-targets-tbody'),
    newTargetName: document.getElementById('new-target-name'),
    newTargetHost: document.getElementById('new-target-host'),
    newTargetCategory: document.getElementById('new-target-category'),
    btnAddTarget: document.getElementById('btn-add-target'),

    // Settings DNS
    cfgDnsEnabled: document.getElementById('cfg-dns-enabled'),
    cfgDnsDomain: document.getElementById('cfg-dns-domain'),
    cfgDnsInterval: document.getElementById('cfg-dns-interval'),
    configDnsTbody: document.getElementById('config-dns-tbody'),
    newDnsName: document.getElementById('new-dns-name'),
    newDnsHost: document.getElementById('new-dns-host'),
    btnAddDns: document.getElementById('btn-add-dns'),

    // Settings Probe Thresholds
    cfgProbeInterval: document.getElementById('cfg-probe-interval'),
    cfgProbePackets: document.getElementById('cfg-probe-packets'),
    cfgProbeTimeout: document.getElementById('cfg-probe-timeout'),
    cfgJitterThreshold: document.getElementById('cfg-jitter-threshold'),
    cfgLossThreshold: document.getElementById('cfg-loss-threshold'),

    // Toast Container
    toastContainer: document.getElementById('toast-container')
  };

  // --- Initializer ---
  async function init() {
    initCharts();
    setupResizeObserver();
    setupEventListeners();

    // Initial data fetch: load config & status first so knownTargets is populated before chart queries
    await loadConfiguration();
    await fetchStatus();
    await Promise.all([
      fetchPingMetrics(),
      fetchDnsMetrics(),
      fetchIncidents()
    ]);

    // Start background polling intervals
    setInterval(fetchStatus, CONFIG.pollIntervalMs);
    setInterval(() => {
      fetchPingMetrics(true);
      fetchDnsMetrics(true);
      fetchIncidents(true);
    }, CONFIG.chartRefreshIntervalMs);

    // Global resize handler
    window.addEventListener('resize', handleChartsResize);
  }

  // --- ECharts 6.1.0 Initialization ---
  function initCharts() {
    if (typeof echarts === 'undefined') {
      console.error('[Tenom] ECharts library not loaded. Check script tag.');
      return;
    }

    // Ping Latency & Loss Chart
    if (DOM.chartPingEl) {
      state.pingChart = echarts.init(DOM.chartPingEl, null, { renderer: 'canvas' });
      state.pingChart.setOption(getEmptyChartOption('Loading latency series...'));
    }

    // DNS Benchmark Chart
    if (DOM.chartDnsEl) {
      state.dnsChart = echarts.init(DOM.chartDnsEl, null, { renderer: 'canvas' });
      state.dnsChart.setOption(getEmptyChartOption('Loading DNS resolution benchmark...'));
    }
  }

  function setupResizeObserver() {
    if (!window.ResizeObserver) return;

    const ro = new ResizeObserver(() => {
      handleChartsResize();
    });

    if (DOM.chartPingEl) ro.observe(DOM.chartPingEl);
    if (DOM.chartDnsEl) ro.observe(DOM.chartDnsEl);
  }

  function handleChartsResize() {
    if (state.pingChart) state.pingChart.resize();
    if (state.dnsChart) state.dnsChart.resize();
  }

  function getEmptyChartOption(message) {
    return {
      backgroundColor: 'transparent',
      title: {
        text: message || 'Awaiting telemetry data...',
        left: 'center',
        top: 'center',
        textStyle: {
          color: '#52525b',
          fontSize: 13,
          fontFamily: 'Inter, sans-serif'
        }
      }
    };
  }

  // --- Event Listeners Setup ---
  function setupEventListeners() {
    // Manual Force Refresh
    if (DOM.btnRefresh) {
      DOM.btnRefresh.addEventListener('click', () => {
        DOM.btnRefresh.classList.add('spinning');
        Promise.all([
          fetchStatus(),
          fetchPingMetrics(),
          fetchDnsMetrics(),
          fetchIncidents()
        ]).finally(() => {
          setTimeout(() => DOM.btnRefresh.classList.remove('spinning'), 600);
        });
      });
    }

    // Target Filter Selector (All Targets vs Individual)
    if (DOM.pingTargetSelect) {
      DOM.pingTargetSelect.addEventListener('change', (e) => {
        state.pingSelectedTarget = e.target.value;
        if (state.lastPingRaw) {
          renderPingChart(state.lastPingRaw);
        } else {
          fetchPingMetrics();
        }
      });
    }

    // Ping Range Selector (1h, 24h, 7d)
    if (DOM.pingRangePicker) {
      DOM.pingRangePicker.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-range]');
        if (!btn || btn.classList.contains('active')) return;

        DOM.pingRangePicker.querySelectorAll('.seg-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        state.pingRange = btn.dataset.range;
        fetchPingMetrics();
      });
    }

    // Ping Metric Toggle (Latency, Loss %, Dual View)
    document.querySelectorAll('.chart-view-toggle button[data-metric]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        const targetBtn = e.target.closest('button[data-metric]');
        if (!targetBtn || targetBtn.classList.contains('active')) return;

        document.querySelectorAll('.chart-view-toggle button[data-metric]').forEach(b => b.classList.remove('active'));
        targetBtn.classList.add('active');
        state.pingMetric = targetBtn.dataset.metric;
        if (state.lastPingRaw) {
          renderPingChart(state.lastPingRaw);
        } else {
          fetchPingMetrics();
        }
      });
    });

    // DNS Range Selector (1h, 24h)
    if (DOM.dnsRangePicker) {
      DOM.dnsRangePicker.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-range]');
        if (!btn || btn.classList.contains('active')) return;

        DOM.dnsRangePicker.querySelectorAll('.seg-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        state.dnsRange = btn.dataset.range;
        fetchDnsMetrics();
      });
    }

    // DNS Mode Toggle (Timeline vs Bar Benchmark)
    if (DOM.dnsViewToggle) {
      DOM.dnsViewToggle.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-mode]');
        if (!btn || btn.classList.contains('active')) return;

        DOM.dnsViewToggle.querySelectorAll('.toggle-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        state.dnsMode = btn.dataset.mode;
        if (state.lastDnsRaw) {
          renderDnsChart(state.lastDnsRaw);
        } else {
          fetchDnsMetrics();
        }
      });
    }

    // Incidents Filter Input
    if (DOM.incidentFilterInput) {
      DOM.incidentFilterInput.addEventListener('input', (e) => {
        filterIncidentsTable(e.target.value.trim().toLowerCase());
      });
    }

    // Modal Close
    if (DOM.btnModalClose) DOM.btnModalClose.addEventListener('click', closeModal);
    if (DOM.tracerouteModal) {
      DOM.tracerouteModal.addEventListener('click', (e) => {
        if (e.target === DOM.tracerouteModal) closeModal();
      });
    }

    // Modal Copy to Clipboard
    if (DOM.btnCopyTraceroute) {
      DOM.btnCopyTraceroute.addEventListener('click', () => {
        if (!state.activeTracerouteText) return;
        navigator.clipboard.writeText(state.activeTracerouteText).then(() => {
          DOM.copyBtnText.textContent = 'Copied!';
          setTimeout(() => {
            DOM.copyBtnText.textContent = 'Copy';
          }, 2000);
        }).catch(err => {
          console.warn('Failed to copy to clipboard:', err);
        });
      });
    }

    // Settings Drawer Open / Close
    if (DOM.btnSettings) {
      DOM.btnSettings.addEventListener('click', openSettingsDrawer);
    }
    if (DOM.btnCloseDrawer) {
      DOM.btnCloseDrawer.addEventListener('click', closeSettingsDrawer);
    }
    if (DOM.settingsBackdrop) {
      DOM.settingsBackdrop.addEventListener('click', closeSettingsDrawer);
    }

    // Settings Drawer Tabs
    document.querySelectorAll('.drawer-tab').forEach(tab => {
      tab.addEventListener('click', () => {
        const targetTab = tab.dataset.tab;
        document.querySelectorAll('.drawer-tab').forEach(t => t.classList.remove('active'));
        document.querySelectorAll('.tab-pane').forEach(p => p.classList.remove('active'));
        tab.classList.add('active');
        const pane = document.getElementById(`tab-content-${targetTab}`);
        if (pane) pane.classList.add('active');
      });
    });

    // Settings: Add Target
    if (DOM.btnAddTarget) {
      DOM.btnAddTarget.addEventListener('click', handleAddTarget);
    }

    // Settings: Add DNS Server
    if (DOM.btnAddDns) {
      DOM.btnAddDns.addEventListener('click', handleAddDnsServer);
    }

    // Settings: Save & Apply
    if (DOM.btnSaveConfig) {
      DOM.btnSaveConfig.addEventListener('click', () => handleSaveConfiguration(true));
    }

    // Settings: Discard
    if (DOM.btnDiscardConfig) {
      DOM.btnDiscardConfig.addEventListener('click', () => {
        if (state.cachedConfig) {
          state.workingConfig = JSON.parse(JSON.stringify(state.cachedConfig));
          populateSettingsForms(state.workingConfig);
          showToast('Configuration reset to active settings', 'success');
        }
      });
    }

    // Keyboard Shortcuts (Esc to close modal or drawer)
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        if (DOM.tracerouteModal && !DOM.tracerouteModal.classList.contains('hidden')) {
          closeModal();
        } else if (DOM.settingsDrawer && !DOM.settingsDrawer.classList.contains('hidden')) {
          closeSettingsDrawer();
        }
      }
    });

    // Main Dashboard Tab Navigation
    document.querySelectorAll('.main-tab-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        const tab = btn.dataset.tab;
        if (tab) handleMainTabSwitch(tab);
      });
    });
  }

  function handleMainTabSwitch(tabName) {
    document.querySelectorAll('.main-tab-btn').forEach(btn => {
      const isActive = btn.dataset.tab === tabName;
      btn.classList.toggle('active', isActive);
      btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });

    document.querySelectorAll('.main-tab-pane').forEach(pane => {
      const isActive = pane.id === `tab-pane-${tabName}`;
      pane.classList.toggle('active', isActive);
    });

    if (tabName === 'overview') {
      setTimeout(() => {
        handleChartsResize();
      }, 50);
    }
  }

  // --- API Fetchers ---

  /**
   * Fetches live health status from /api/v1/status
   */
  async function fetchStatus() {
    try {
      const res = await fetch(CONFIG.endpoints.status);
      if (!res.ok) throw new Error(`HTTP error ${res.status}`);
      const data = await res.json();
      state.statusData = data;
      renderStatusOverview(data);
      updateLastSyncTime();
    } catch (err) {
      console.warn('[Tenom] Status fetch error:', err);
      renderStatusError(err);
    }
  }

  /**
   * Fetches Ping Latency metrics from /api/v1/metrics/ping?range=...
   */
  async function fetchPingMetrics(isBackground = false) {
    try {
      if (!state.knownTargets || state.knownTargets.length === 0) {
        if (state.cachedConfig && state.cachedConfig.targets) {
          state.knownTargets = state.cachedConfig.targets;
        } else {
          await fetchStatus();
        }
      }

      const targets = state.knownTargets || [];
      if (targets.length === 0) {
        if (state.pingChart) state.pingChart.setOption(getEmptyChartOption('No probe targets configured.'));
        return;
      }

      const multiData = await fetchAllTargetsPing(state.pingRange, targets);
      state.lastPingRaw = multiData;
      renderPingChart(multiData);
    } catch (err) {
      console.warn('[Tenom] Ping metrics fetch error:', err);
      if (!isBackground && state.pingChart) {
        state.pingChart.setOption(getEmptyChartOption('Unable to load ping metrics.'));
      }
    }
  }

  /**
   * Parallel fetcher for targets dynamically derived from live targets
   */
  async function fetchAllTargetsPing(range, targets) {
    const seriesMap = {};
    const lossMap = {};
    const timestampSet = new Set();

    const promises = targets.map(async (t) => {
      const host = t.target || t.host;
      if (!host) return { target: '', points: [] };
      try {
        const r = await fetch(`${CONFIG.endpoints.pingMetrics}?range=${range}&target=${encodeURIComponent(host)}`);
        if (r.ok) {
          const pts = await r.json();
          return { target: host, points: pts || [] };
        }
      } catch (e) {
        // Individual target failure
      }
      return { target: host, points: [] };
    });

    const allResults = await Promise.all(promises);

    allResults.forEach(res => {
      if (!res.target) return;
      res.points.forEach(pt => timestampSet.add(formatTimestampKey(pt.timestamp)));
    });

    const sortedTimestamps = Array.from(timestampSet).sort();

    allResults.forEach(res => {
      if (!res.target) return;
      const pointMap = {};
      res.points.forEach(pt => {
        pointMap[formatTimestampKey(pt.timestamp)] = pt;
      });

      seriesMap[res.target] = sortedTimestamps.map(ts => {
        const pt = pointMap[ts];
        return pt ? (pt.avg_rtt ?? pt.rtt_ms ?? null) : null;
      });

      lossMap[res.target] = sortedTimestamps.map(ts => {
        const pt = pointMap[ts];
        return pt ? (pt.packet_loss ?? 0) : 0;
      });
    });

    return {
      timestamps: sortedTimestamps,
      series: seriesMap,
      packet_loss: lossMap
    };
  }

  /**
   * Fetches DNS benchmark metrics from /api/v1/metrics/dns?range=...
   */
  async function fetchDnsMetrics(isBackground = false) {
    try {
      const url = `${CONFIG.endpoints.dnsMetrics}?range=${state.dnsRange}`;
      const res = await fetch(url);
      if (!res.ok) throw new Error(`HTTP error ${res.status}`);
      const data = await res.json();
      state.lastDnsRaw = data;
      renderDnsChart(data);
    } catch (err) {
      console.warn('[Tenom] DNS metrics fetch error:', err);
      if (!isBackground && state.dnsChart) {
        state.dnsChart.setOption(getEmptyChartOption('Unable to load DNS metrics.'));
      }
    }
  }

  /**
   * Fetches Incident history from /api/v1/incidents
   */
  async function fetchIncidents(isBackground = false) {
    try {
      const res = await fetch(CONFIG.endpoints.incidents);
      if (!res.ok) throw new Error(`HTTP error ${res.status}`);
      const data = await res.json();

      let incidentsList = [];
      if (Array.isArray(data)) {
        incidentsList = data;
      } else if (data && Array.isArray(data.incidents)) {
        incidentsList = data.incidents;
      } else if (data && Array.isArray(data.traceroutes)) {
        incidentsList = data.traceroutes;
      }

      state.incidents = incidentsList;
      renderIncidentsTable(incidentsList);
    } catch (err) {
      console.warn('[Tenom] Incidents fetch error:', err);
      if (!isBackground && DOM.incidentsTbody) {
        DOM.incidentsTbody.innerHTML = `<tr><td colspan="6" class="text-center" style="color: #f87171;">Failed to load incidents log.</td></tr>`;
      }
    }
  }

  // --- Renderers ---

  /**
   * Renders the Overview Cards, System Status Badge, and Target Strip
   */
  function renderStatusOverview(data) {
    if (!data) return;

    const rawStatus = (data.status || data.state || 'OPERATIONAL').toUpperCase();
    const isOutage = data.active_outage === true || rawStatus.includes('OUTAGE') || rawStatus.includes('DOWN') || rawStatus.includes('CRITICAL');
    const isDegraded = !isOutage && (rawStatus.includes('DEGRADED') || rawStatus.includes('WARNING'));

    let normalizedStatus = 'OPERATIONAL';
    let badgeClass = 'status-operational';
    let statusText = 'All Systems Operational';
    let valTextClass = 'status-text-green';
    let accentBarClass = 'accent-emerald';

    if (isOutage) {
      normalizedStatus = 'OUTAGE';
      badgeClass = 'status-outage';
      statusText = 'Outage Detected';
      valTextClass = 'status-text-red';
      accentBarClass = 'accent-red';
    } else if (isDegraded) {
      normalizedStatus = 'DEGRADED';
      badgeClass = 'status-degraded';
      statusText = 'Performance Degraded';
      valTextClass = 'status-text-amber';
      accentBarClass = 'accent-amber';
    }

    // Update Header Badge
    DOM.systemStatusBadge.className = `status-badge ${badgeClass}`;
    DOM.systemStatusText.textContent = statusText;

    // Update KPI Card 1: System State
    DOM.kpiStateVal.textContent = normalizedStatus;
    DOM.kpiStateVal.className = `kpi-value ${valTextClass}`;
    DOM.kpiStateBar.className = `card-accent-bar ${accentBarClass}`;
    DOM.kpiStateDesc.textContent = isOutage
      ? 'Critical packet drop or SLA violation'
      : isDegraded
      ? 'Elevated jitter or partial packet drops'
      : 'All probe targets reachable & healthy';

    // Outage Banner & Counter
    handleOutageTimer(data, isOutage);

    // 24h Uptime Counter
    const uptime = data.uptime_24h ?? data.uptime_percentage ?? 100.0;
    const formattedUptime = Number(uptime).toFixed(2);
    DOM.kpiUptimeVal.innerHTML = `${formattedUptime}<small class="kpi-unit">%</small>`;
    if (DOM.slaProgressBar) {
      const clamped = Math.max(0, Math.min(100, uptime));
      DOM.slaProgressBar.style.width = `${clamped}%`;
    }

    // Targets Snapshot & Target Cards
    const targets = Array.isArray(data.targets) ? data.targets : [];

    // Enrich targets with config names if available
    if (state.cachedConfig && state.cachedConfig.targets) {
      const nameMap = {};
      state.cachedConfig.targets.forEach(ct => {
        if (ct.host) nameMap[ct.host] = ct.name;
      });
      targets.forEach(t => {
        const host = t.target || t.host;
        if (host && nameMap[host]) t.name = nameMap[host];
      });
    }

    state.knownTargets = targets;
    DOM.targetsCount.textContent = `${targets.length} target${targets.length === 1 ? '' : 's'}`;
    const tabTargetsCount = document.getElementById('tab-targets-count');
    if (tabTargetsCount) tabTargetsCount.textContent = targets.length;

    // Update Ping Target Dropdown filter if target list changed
    updateTargetFilterDropdown(targets);

    let gatewayTarget = null;
    let wanTarget = null;
    let dnsTargetAvg = 0;
    let dnsSampleCount = 0;

    targets.forEach(t => {
      const host = t.target || t.host || '';
      const cat = (t.category || '').toUpperCase();

      if (!gatewayTarget && (cat.includes('LAN') || host.startsWith('192.168.') || host.startsWith('10.') || host.toLowerCase().includes('gateway'))) {
        gatewayTarget = t;
      } else if (!wanTarget && (cat.includes('ANYCAST') || cat.includes('WAN') || host.toLowerCase().includes('cloudflare') || host === '1.1.1.1')) {
        wanTarget = t;
      }

      if (host.includes(':53') || cat.includes('DNS')) {
        dnsTargetAvg += (t.rtt_ms || 0);
        dnsSampleCount++;
      }
    });

    if (!gatewayTarget && targets.length > 0) gatewayTarget = targets[0];
    if (!wanTarget && targets.length > 1) wanTarget = targets[1];

    if (gatewayTarget) {
      const rtt = Number(gatewayTarget.rtt_ms || 0).toFixed(2);
      const loss = Number(gatewayTarget.packet_loss || 0).toFixed(1);
      const label = gatewayTarget.name ? `${gatewayTarget.name} (${gatewayTarget.target || gatewayTarget.host})` : (gatewayTarget.target || gatewayTarget.host || 'LAN Gateway');
      DOM.kpiGatewayVal.innerHTML = `${rtt} <small class="kpi-unit">ms</small>`;
      DOM.kpiGatewayHost.textContent = `Target: ${label}`;
      DOM.kpiGatewayLoss.textContent = `${loss}% loss`;
      DOM.kpiGatewayLoss.className = `badge-pill ${loss > 0 ? 'badge-red' : 'badge-green'} mono`;
    }

    if (wanTarget) {
      const rtt = Number(wanTarget.rtt_ms || 0).toFixed(2);
      const loss = Number(wanTarget.packet_loss || 0).toFixed(1);
      const label = wanTarget.name ? `${wanTarget.name} (${wanTarget.target || wanTarget.host})` : (wanTarget.target || wanTarget.host || 'WAN Gateway');
      DOM.kpiWanVal.innerHTML = `${rtt} <small class="kpi-unit">ms</small>`;
      DOM.kpiWanHost.textContent = `Target: ${label}`;
      DOM.kpiWanLoss.textContent = `${loss}% loss`;
      DOM.kpiWanLoss.className = `badge-pill ${loss > 0 ? 'badge-red' : 'badge-green'} mono`;
    }

    if (dnsSampleCount > 0) {
      const avg = (dnsTargetAvg / dnsSampleCount).toFixed(1);
      DOM.kpiDnsVal.innerHTML = `${avg} <small class="kpi-unit">ms</small>`;
    }

    renderTargetsStrip(targets);
  }

  function updateTargetFilterDropdown(targets) {
    if (!DOM.pingTargetSelect) return;
    const currentVal = DOM.pingTargetSelect.value;
    const hosts = targets.map(t => t.target || t.host).filter(Boolean);

    let html = `<option value="all">All Targets (${hosts.length})</option>`;
    targets.forEach(t => {
      const host = t.target || t.host;
      const name = t.name ? `${t.name} (${host})` : host;
      html += `<option value="${escapeHTML(host)}">${escapeHTML(name)}</option>`;
    });

    DOM.pingTargetSelect.innerHTML = html;
    if (hosts.includes(currentVal)) {
      DOM.pingTargetSelect.value = currentVal;
    } else {
      DOM.pingTargetSelect.value = 'all';
      state.pingSelectedTarget = 'all';
    }
  }

  function handleOutageTimer(data, isOutage) {
    if (isOutage) {
      DOM.outageBanner.classList.remove('hidden');
      const serverSec = data.current_outage_duration_s ?? data.active_outage_duration_seconds ?? 0;
      state.outageDurationSeconds = Math.max(state.outageDurationSeconds, Math.round(serverSec));

      if (!state.outageTimerId) {
        state.outageTimerId = setInterval(() => {
          state.outageDurationSeconds++;
          DOM.outageDurationCounter.textContent = formatDuration(state.outageDurationSeconds);
        }, 1000);
      }
      DOM.outageDurationCounter.textContent = formatDuration(state.outageDurationSeconds);
    } else {
      DOM.outageBanner.classList.add('hidden');
      if (state.outageTimerId) {
        clearInterval(state.outageTimerId);
        state.outageTimerId = null;
      }
      state.outageDurationSeconds = 0;
    }
  }

  function renderTargetsStrip(targets) {
    if (!DOM.targetsContainer) return;

    if (!targets || targets.length === 0) {
      DOM.targetsContainer.innerHTML = `
        <div class="target-card" style="grid-column: 1 / -1; justify-content: center; color: var(--text-muted);">
          No probe targets configured. Click the gear icon to add hosts.
        </div>`;
      return;
    }

    DOM.targetsContainer.innerHTML = targets.map(t => {
      const host = escapeHTML(t.target || t.host || 'Unknown');
      const name = escapeHTML(t.name || host);
      const cat = escapeHTML(t.category || 'Target');
      const rtt = Number(t.rtt_ms || 0).toFixed(2);
      const loss = Number(t.packet_loss || 0).toFixed(1);
      const isLoss = Number(t.packet_loss || 0) > 0;
      const dotColor = isLoss ? '#ef4444' : (t.rtt_ms > 80 ? '#f59e0b' : '#10b981');
      const sampleTime = t.timestamp ? formatTimeOnly(t.timestamp) : 'Live';

      let catBadgeClass = 'badge-neutral';
      if (cat.includes('LAN')) catBadgeClass = 'badge-blue';
      else if (cat.includes('ANYCAST')) catBadgeClass = 'badge-purple';
      else if (cat.includes('GAME')) catBadgeClass = 'badge-yellow';

      return `
        <div class="target-card" data-target="${host}">
          <div class="target-info">
            <div class="target-host-row">
              <span class="status-dot" style="background-color: ${dotColor}; box-shadow: 0 0 6px ${dotColor};"></span>
              <span class="target-host mono">${host}</span>
              <span class="target-category mono ${catBadgeClass}">${cat}</span>
            </div>
            <span class="target-meta-time mono">${name} · ${sampleTime}</span>
          </div>
          <div class="target-metrics">
            <span class="target-rtt mono">${rtt} <small style="font-size: 0.75rem; color: var(--text-muted);">ms</small></span>
            <span class="badge-pill ${isLoss ? 'badge-red' : 'badge-green'} mono">${loss}% loss</span>
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Renders the main Ping Latency & Loss Chart using ECharts 6.1.0
   * Implements scrolling legend, exact grid layout, and multi-target filtering
   */
  function renderPingChart(raw) {
    if (!state.pingChart) return;

    const parsed = parsePingMetrics(raw);
    if (!parsed || parsed.timestamps.length === 0) {
      DOM.chartPingEmpty.classList.remove('hidden');
      state.pingChart.clear();
      return;
    }
    DOM.chartPingEmpty.classList.add('hidden');

    let { timestamps, seriesList, lossList } = parsed;
    const viewMetric = state.pingMetric;
    const filterTarget = state.pingSelectedTarget;

    // Filter series if an individual target is selected
    if (filterTarget && filterTarget !== 'all') {
      seriesList = seriesList.filter(s => s.target === filterTarget);
      lossList = lossList.filter(l => l.target === filterTarget);
    }

    const chartSeries = [];
    const yAxisConfig = [];
    const legendData = [];

    // Left Y-Axis: Latency (ms)
    yAxisConfig.push({
      type: 'value',
      name: 'RTT (ms)',
      position: 'left',
      nameTextStyle: { color: '#a1a1aa', fontSize: 11, fontFamily: 'Inter' },
      axisLine: { lineStyle: { color: '#27272a' } },
      axisLabel: { color: '#71717a', fontSize: 11, fontFamily: 'JetBrains Mono' },
      splitLine: { lineStyle: { color: '#27272a', type: 'dashed' } }
    });

    // Right Y-Axis: Packet Loss (%)
    if (viewMetric === 'loss' || viewMetric === 'both') {
      yAxisConfig.push({
        type: 'value',
        name: 'Packet Loss (%)',
        position: 'right',
        min: 0,
        max: 100,
        nameTextStyle: { color: '#f87171', fontSize: 11, fontFamily: 'Inter' },
        axisLine: { lineStyle: { color: '#27272a' } },
        axisLabel: {
          color: '#f87171',
          fontSize: 11,
          fontFamily: 'JetBrains Mono',
          formatter: '{value}%'
        },
        splitLine: { show: false }
      });
    }

    // Build series
    seriesList.forEach((s, idx) => {
      const color = SERIES_PALETTE[idx % SERIES_PALETTE.length];
      const targetName = s.target;

      if (viewMetric === 'rtt' || viewMetric === 'both') {
        legendData.push(targetName);
        chartSeries.push({
          name: targetName,
          type: 'line',
          yAxisIndex: 0,
          smooth: 0.35,
          showSymbol: false,
          symbolSize: 6,
          lineStyle: { width: 2.2, color: color },
          itemStyle: { color: color },
          areaStyle: {
            color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
              { offset: 0, color: hexToRgba(color, 0.22) },
              { offset: 1, color: hexToRgba(color, 0.0) }
            ])
          },
          data: s.data
        });
      }

      if (viewMetric === 'loss' || viewMetric === 'both') {
        const lossSeriesName = `${targetName} Loss`;
        legendData.push(lossSeriesName);
        const targetLossObj = lossList.find(l => l.target === targetName);
        const lossData = (targetLossObj ? targetLossObj.data : s.lossData) || [];

        chartSeries.push({
          name: lossSeriesName,
          type: 'line',
          yAxisIndex: viewMetric === 'both' ? 1 : 0,
          step: 'middle',
          showSymbol: false,
          lineStyle: { width: 1.5, type: 'dashed', color: '#ef4444' },
          itemStyle: { color: '#ef4444' },
          data: lossData
        });
      }
    });

    const option = {
      backgroundColor: 'transparent',
      animationDuration: 350,
      tooltip: {
        trigger: 'axis',
        backgroundColor: 'rgba(24, 24, 27, 0.96)',
        borderColor: '#3f3f46',
        borderWidth: 1,
        padding: [10, 14],
        textStyle: { color: '#f4f4f5', fontFamily: 'JetBrains Mono', fontSize: 12 },
        extraCssText: 'box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.7); backdrop-filter: blur(8px); border-radius: 8px;',
        formatter: function (params) {
          if (!params || params.length === 0) return '';
          let header = `<div style="font-weight:600; color:#fafafa; margin-bottom:6px; font-family:Inter, sans-serif; border-bottom:1px solid #3f3f46; padding-bottom:4px;">${params[0].axisValueLabel}</div>`;
          let rows = params.map(p => {
            const isLossSeries = p.seriesName.includes('Loss');
            const unit = isLossSeries ? '%' : 'ms';
            const val = p.value !== null && p.value !== undefined ? `${Number(p.value).toFixed(2)} ${unit}` : 'N/A';
            const isHighLoss = isLossSeries && p.value > 0;
            const valStyle = isHighLoss ? 'color:#ef4444; font-weight:700;' : 'color:#f4f4f5;';

            return `
              <div style="display:flex; align-items:center; justify-content:space-between; gap:1.5rem; margin-top:3px;">
                <span style="display:flex; align-items:center; gap:6px;">
                  <span style="display:inline-block; width:8px; height:8px; border-radius:50%; background-color:${p.color};"></span>
                  <span style="color:#a1a1aa;">${escapeHTML(p.seriesName)}:</span>
                </span>
                <span style="${valStyle}">${val}</span>
              </div>
            `;
          }).join('');
          return header + rows;
        }
      },
      // Configured for 5-20 targets with horizontal scroll
      legend: {
        type: 'scroll',
        orient: 'horizontal',
        top: 10,
        pageIconColor: '#a1a1aa',
        pageTextStyle: { color: '#a1a1aa' },
        textStyle: { color: '#a1a1aa', fontSize: 11, fontFamily: 'Inter' },
        icon: 'circle',
        data: legendData
      },
      // Explicit grid requirement
      grid: {
        top: 60,
        bottom: 50,
        left: 55,
        right: 25,
        containLabel: false
      },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: timestamps,
        axisLine: { lineStyle: { color: '#27272a' } },
        axisLabel: {
          color: '#71717a',
          fontSize: 11,
          fontFamily: 'JetBrains Mono',
          formatter: formatChartXAxisLabel
        }
      },
      yAxis: yAxisConfig,
      dataZoom: [
        {
          type: 'slider',
          show: true,
          xAxisIndex: [0],
          bottom: 4,
          height: 20,
          borderColor: '#27272a',
          backgroundColor: '#121215',
          fillerColor: 'rgba(6, 182, 212, 0.15)',
          handleStyle: {
            color: '#06b6d4',
            borderColor: '#06b6d4'
          },
          textStyle: { color: '#71717a', fontSize: 10, fontFamily: 'JetBrains Mono' },
          brushSelect: false
        },
        {
          type: 'inside',
          xAxisIndex: [0]
        }
      ],
      series: chartSeries
    };

    state.pingChart.setOption(option, true);
  }

  function parsePingMetrics(raw) {
    if (!raw) return null;

    if (raw.timestamps && raw.series) {
      const timestamps = raw.timestamps.map(formatTimestampLabel);
      const seriesList = [];
      const lossList = [];

      Object.keys(raw.series).forEach(target => {
        const val = raw.series[target];
        let rttData = [];
        let lossData = [];

        if (Array.isArray(val)) {
          rttData = val;
        } else if (val && typeof val === 'object') {
          rttData = val.rtt || val.rtt_ms || [];
          lossData = val.loss || val.packet_loss || [];
        }

        seriesList.push({ target, data: rttData });

        if (raw.packet_loss && raw.packet_loss[target]) {
          lossList.push({ target, data: raw.packet_loss[target] });
        } else if (lossData.length > 0) {
          lossList.push({ target, data: lossData });
        }
      });

      return { timestamps, seriesList, lossList };
    }

    if (Array.isArray(raw)) {
      if (raw.length === 0) return { timestamps: [], seriesList: [], lossList: [] };

      const timestamps = [];
      const rttData = [];
      const lossData = [];

      raw.forEach(pt => {
        timestamps.push(formatTimestampLabel(pt.timestamp));
        rttData.push(pt.avg_rtt ?? pt.rtt_ms ?? 0);
        lossData.push(pt.packet_loss ?? 0);
      });

      const primaryTarget = state.knownTargets[0]?.target || state.knownTargets[0]?.host || 'Primary';
      return {
        timestamps,
        seriesList: [{ target: primaryTarget, data: rttData }],
        lossList: [{ target: primaryTarget, data: lossData }]
      };
    }

    return null;
  }

  /**
   * Renders the DNS Benchmark Chart using ECharts 6.1.0
   */
  function renderDnsChart(raw) {
    if (!state.dnsChart) return;

    const parsed = parseDnsMetrics(raw);
    if (!parsed || parsed.servers.length === 0) {
      DOM.chartDnsEmpty.classList.remove('hidden');
      state.dnsChart.clear();
      return;
    }
    DOM.chartDnsEmpty.classList.add('hidden');

    const { servers, serverAverages, timestamps, timelineSeries } = parsed;

    let fastestServer = null;
    let lowestLat = Infinity;
    let totalLat = 0;
    let count = 0;

    Object.keys(serverAverages).forEach(srv => {
      const avg = serverAverages[srv];
      totalLat += avg;
      count++;
      if (avg < lowestLat) {
        lowestLat = avg;
        fastestServer = srv;
      }
    });

    if (count > 0) {
      const overallAvg = (totalLat / count).toFixed(1);
      DOM.kpiDnsVal.innerHTML = `${overallAvg} <small class="kpi-unit">ms</small>`;
      if (fastestServer) {
        DOM.kpiDnsFastest.textContent = `${fastestServer} (${lowestLat.toFixed(1)}ms)`;
      }
    }

    // Mode A: Bar Comparison
    if (state.dnsMode === 'bar') {
      const barData = servers.map(srv => serverAverages[srv] || 0);

      const option = {
        backgroundColor: 'transparent',
        animationDuration: 350,
        tooltip: {
          trigger: 'axis',
          axisPointer: { type: 'shadow' },
          backgroundColor: 'rgba(24, 24, 27, 0.96)',
          borderColor: '#3f3f46',
          textStyle: { color: '#f4f4f5', fontFamily: 'JetBrains Mono', fontSize: 12 },
          formatter: '{b}: <b>{c} ms</b>'
        },
        grid: {
          top: 40,
          bottom: 30,
          left: 55,
          right: 25,
          containLabel: false
        },
        xAxis: {
          type: 'category',
          data: servers,
          axisLine: { lineStyle: { color: '#27272a' } },
          axisLabel: { color: '#a1a1aa', fontSize: 11, fontFamily: 'Inter' }
        },
        yAxis: {
          type: 'value',
          name: 'Latency (ms)',
          nameTextStyle: { color: '#71717a', fontSize: 11 },
          axisLine: { lineStyle: { color: '#27272a' } },
          axisLabel: { color: '#71717a', fontSize: 11, fontFamily: 'JetBrains Mono' },
          splitLine: { lineStyle: { color: '#27272a', type: 'dashed' } }
        },
        series: [
          {
            name: 'Lookup Latency',
            type: 'bar',
            barWidth: '40%',
            data: barData.map((val, idx) => ({
              value: val,
              itemStyle: {
                color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                  { offset: 0, color: SERIES_PALETTE[idx % SERIES_PALETTE.length] },
                  { offset: 1, color: hexToRgba(SERIES_PALETTE[idx % SERIES_PALETTE.length], 0.3) }
                ]),
                borderRadius: [6, 6, 0, 0]
              }
            }))
          }
        ]
      };
      state.dnsChart.setOption(option, true);
      return;
    }

    // Mode B: Timeline Multi-Line
    const option = {
      backgroundColor: 'transparent',
      animationDuration: 350,
      tooltip: {
        trigger: 'axis',
        backgroundColor: 'rgba(24, 24, 27, 0.96)',
        borderColor: '#3f3f46',
        textStyle: { color: '#f4f4f5', fontFamily: 'JetBrains Mono', fontSize: 12 },
        formatter: function (params) {
          if (!params || params.length === 0) return '';
          let header = `<div style="font-weight:600; color:#fafafa; margin-bottom:6px; font-family:Inter; border-bottom:1px solid #3f3f46; padding-bottom:4px;">${params[0].axisValueLabel}</div>`;
          let rows = params.map(p => `
            <div style="display:flex; justify-content:space-between; gap:1.5rem; margin-top:3px;">
              <span style="color:#a1a1aa;">${escapeHTML(p.seriesName)}:</span>
              <span style="color:#f4f4f5; font-weight:600;">${p.value !== null ? p.value + ' ms' : 'N/A'}</span>
            </div>
          `).join('');
          return header + rows;
        }
      },
      legend: {
        type: 'scroll',
        orient: 'horizontal',
        top: 10,
        pageIconColor: '#a1a1aa',
        pageTextStyle: { color: '#a1a1aa' },
        textStyle: { color: '#a1a1aa', fontSize: 11, fontFamily: 'Inter' },
        icon: 'circle',
        data: servers
      },
      grid: {
        top: 60,
        bottom: 50,
        left: 55,
        right: 25,
        containLabel: false
      },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: timestamps,
        axisLine: { lineStyle: { color: '#27272a' } },
        axisLabel: {
          color: '#71717a',
          fontSize: 11,
          fontFamily: 'JetBrains Mono',
          formatter: formatChartXAxisLabel
        }
      },
      yAxis: {
        type: 'value',
        name: 'Lookup (ms)',
        nameTextStyle: { color: '#71717a', fontSize: 11 },
        axisLine: { lineStyle: { color: '#27272a' } },
        axisLabel: { color: '#71717a', fontSize: 11, fontFamily: 'JetBrains Mono' },
        splitLine: { lineStyle: { color: '#27272a', type: 'dashed' } }
      },
      series: timelineSeries.map((s, idx) => {
        const color = SERIES_PALETTE[idx % SERIES_PALETTE.length];
        return {
          name: s.server,
          type: 'line',
          smooth: 0.35,
          showSymbol: false,
          lineStyle: { width: 2, color: color },
          itemStyle: { color: color },
          data: s.data
        };
      })
    };

    state.dnsChart.setOption(option, true);
  }

  function parseDnsMetrics(raw) {
    if (!raw) return null;

    if (raw.timestamps && raw.series) {
      const timestamps = raw.timestamps.map(formatTimestampLabel);
      const servers = Object.keys(raw.series);
      const serverAverages = {};
      const timelineSeries = [];

      servers.forEach(srv => {
        const vals = raw.series[srv] || [];
        timelineSeries.push({ server: srv, data: vals });

        const valid = vals.filter(v => v !== null && v > 0);
        const sum = valid.reduce((acc, v) => acc + v, 0);
        serverAverages[srv] = valid.length > 0 ? (sum / valid.length) : 0;
      });

      return { servers, serverAverages, timestamps, timelineSeries };
    }

    if (Array.isArray(raw)) {
      if (raw.length === 0) return { servers: [], serverAverages: {}, timestamps: [], timelineSeries: [] };

      const timeSet = new Set();
      const serverMap = {};

      raw.forEach(pt => {
        const t = formatTimestampKey(pt.timestamp);
        timeSet.add(t);
        if (!serverMap[pt.server]) serverMap[pt.server] = {};
        serverMap[pt.server][t] = pt.latency_ms;
      });

      const sortedTimestamps = Array.from(timeSet).sort();
      const formattedTimestamps = sortedTimestamps.map(formatTimestampLabel);
      const servers = Object.keys(serverMap);
      const serverAverages = {};
      const timelineSeries = [];

      servers.forEach(srv => {
        const points = serverMap[srv];
        let total = 0;
        let count = 0;
        const data = sortedTimestamps.map(ts => {
          const v = points[ts];
          if (v !== undefined && v !== null) {
            total += v;
            count++;
            return v;
          }
          return null;
        });

        serverAverages[srv] = count > 0 ? (total / count) : 0;
        timelineSeries.push({ server: srv, data });
      });

      return { servers, serverAverages, timestamps: formattedTimestamps, timelineSeries };
    }

    return null;
  }

  /**
   * Renders the Incidents & Traceroute Log Table
   */
  function renderIncidentsTable(incidents) {
    if (!DOM.incidentsTbody) return;

    DOM.incidentBadgeCount.textContent = `${incidents.length} Event${incidents.length === 1 ? '' : 's'}`;
    const tabIncidentsCount = document.getElementById('tab-incidents-count');
    if (tabIncidentsCount) tabIncidentsCount.textContent = incidents.length;

    if (!incidents || incidents.length === 0) {
      DOM.incidentsTbody.innerHTML = '';
      DOM.incidentsEmptyState.classList.remove('hidden');
      return;
    }
    DOM.incidentsEmptyState.classList.add('hidden');

    DOM.incidentsTbody.innerHTML = incidents.map(inc => {
      const isResolved = inc.resolved_at !== null && inc.resolved_at !== undefined;
      const statusBadge = isResolved
        ? `<span class="badge-pill badge-green mono">RESOLVED</span>`
        : `<span class="badge-pill badge-red mono">ACTIVE</span>`;

      const startedFormatted = formatDateTime(inc.started_at);
      const relativeTime = getRelativeTime(inc.started_at);
      const target = escapeHTML(inc.target || 'N/A');
      const reason = escapeHTML(inc.trigger_reason || 'Outage threshold exceeded');
      const duration = calculateDurationStr(inc.started_at, inc.resolved_at);

      return `
        <tr data-id="${inc.id}">
          <td>${statusBadge}</td>
          <td>
            <div class="mono" style="color: #f4f4f5; font-size: 0.8rem;">${startedFormatted}</div>
            <div style="font-size: 0.72rem; color: #71717a;">${relativeTime}</div>
          </td>
          <td><span class="mono" style="color: #38bdf8; font-weight: 500;">${target}</span></td>
          <td>
            <div style="font-weight: 500; color: #e4e4e7;">${reason}</div>
          </td>
          <td class="mono" style="color: #a1a1aa;">${duration}</td>
          <td style="text-align: right;">
            <button class="btn-trace-action" data-action="view-trace" data-id="${inc.id}">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="4 17 10 11 4 5"></polyline>
                <line x1="12" y1="19" x2="20" y2="19"></line>
              </svg>
              View Trace
            </button>
          </td>
        </tr>
      `;
    }).join('');

    DOM.incidentsTbody.querySelectorAll('tr[data-id]').forEach(row => {
      row.addEventListener('click', () => {
        const id = Number(row.dataset.id);
        const inc = state.incidents.find(item => item.id === id);
        if (inc) openTracerouteModal(inc);
      });
    });
  }

  function filterIncidentsTable(query) {
    if (!DOM.incidentsTbody) return;
    const rows = DOM.incidentsTbody.querySelectorAll('tr[data-id]');
    let visibleCount = 0;

    rows.forEach(row => {
      const text = row.innerText.toLowerCase();
      if (!query || text.includes(query)) {
        row.style.display = '';
        visibleCount++;
      } else {
        row.style.display = 'none';
      }
    });

    if (visibleCount === 0 && rows.length > 0) {
      DOM.incidentsEmptyState.classList.remove('hidden');
    } else if (rows.length > 0) {
      DOM.incidentsEmptyState.classList.add('hidden');
    }
  }

  // --- Modal Management & Traceroute Syntax Highlighter ---

  function openTracerouteModal(incident) {
    if (!incident || !DOM.tracerouteModal) return;

    state.activeTracerouteText = incident.traceroute_output || 'No traceroute hop telemetry captured.';

    DOM.modalCommand.textContent = `traceroute -n ${incident.target || 'target'}`;
    DOM.modalIncidentId.textContent = `#${incident.id}`;
    DOM.modalIncidentReason.textContent = incident.trigger_reason || 'Outage Threshold';
    DOM.modalIncidentTime.textContent = formatDateTime(incident.started_at);

    DOM.modalTracerouteCode.innerHTML = highlightTraceroute(state.activeTracerouteText);
    DOM.copyBtnText.textContent = 'Copy';

    DOM.tracerouteModal.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  function closeModal() {
    if (!DOM.tracerouteModal) return;
    DOM.tracerouteModal.classList.add('hidden');
    document.body.style.overflow = '';
  }

  function highlightTraceroute(raw) {
    if (!raw) return '<span style="color: #71717a;">(Empty output)</span>';

    const lines = raw.split('\n');
    const formatted = lines.map(line => {
      let l = escapeHTML(line);

      // Hop index at start of line
      l = l.replace(/^(\s*\d+)\s+/, '<span class="hop-num">$1</span>  ');

      // IP addresses (IPv4 & IPv6)
      l = l.replace(/\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b/g, '<span class="hop-ip">$1</span>');

      // Latencies ms
      l = l.replace(/(\d+\.\d+)\s*ms/g, (match, val) => {
        const ms = parseFloat(val);
        const cls = ms < 50 ? 'hop-ms-fast' : 'hop-ms-slow';
        return `<span class="${cls}">${val} ms</span>`;
      });

      // Timeouts / Packet Drops
      l = l.replace(/(\*\s*)+/g, '<span class="hop-timeout">*** (Timeout) </span>');

      return l;
    });

    return formatted.join('\n');
  }

  // --- Configuration Drawer Management (Settings CRUD) ---

  async function loadConfiguration() {
    try {
      const res = await fetch(CONFIG.endpoints.config);
      if (!res.ok) throw new Error(`HTTP error ${res.status}`);
      const cfg = await res.json();
      state.cachedConfig = cfg;
      state.workingConfig = JSON.parse(JSON.stringify(cfg));
    } catch (err) {
      console.warn('[Tenom] Could not load initial config:', err);
    }
  }

  function openSettingsDrawer() {
    if (!DOM.settingsDrawer || !DOM.settingsBackdrop) return;

    if (state.cachedConfig) {
      state.workingConfig = JSON.parse(JSON.stringify(state.cachedConfig));
      populateSettingsForms(state.workingConfig);
    } else {
      loadConfiguration().then(() => {
        if (state.workingConfig) populateSettingsForms(state.workingConfig);
      });
    }

    DOM.settingsBackdrop.classList.remove('hidden');
    DOM.settingsDrawer.classList.remove('hidden');
    document.body.style.overflow = 'hidden';

    setTimeout(() => {
      handleChartsResize();
    }, 320);
  }

  function closeSettingsDrawer() {
    if (!DOM.settingsDrawer || !DOM.settingsBackdrop) return;
    DOM.settingsBackdrop.classList.add('hidden');
    DOM.settingsDrawer.classList.add('hidden');
    document.body.style.overflow = '';

    setTimeout(() => {
      handleChartsResize();
    }, 320);
  }

  function populateSettingsForms(cfg) {
    if (!cfg) return;

    // 1. Targets List
    renderConfigTargetsTable(cfg.targets || []);

    // 2. DNS Settings
    if (DOM.cfgDnsEnabled) DOM.cfgDnsEnabled.checked = cfg.dns?.enabled ?? true;
    if (DOM.cfgDnsDomain) DOM.cfgDnsDomain.value = cfg.dns?.domain || 'google.com';
    if (DOM.cfgDnsInterval) DOM.cfgDnsInterval.value = cfg.dns?.interval_seconds || 30;
    renderConfigDnsTable(cfg.dns?.servers || []);

    // 3. Probe Thresholds
    if (DOM.cfgProbeInterval) DOM.cfgProbeInterval.value = cfg.probe?.interval_seconds || 15;
    if (DOM.cfgProbePackets) DOM.cfgProbePackets.value = cfg.probe?.packet_count || 3;
    if (DOM.cfgProbeTimeout) DOM.cfgProbeTimeout.value = cfg.probe?.timeout_seconds || 3;
    if (DOM.cfgJitterThreshold) DOM.cfgJitterThreshold.value = cfg.probe?.jitter_threshold_ms || 120.0;
    if (DOM.cfgLossThreshold) DOM.cfgLossThreshold.value = cfg.probe?.loss_threshold_pct || 10.0;
  }

  function renderConfigTargetsTable(targets) {
    if (!DOM.configTargetsTbody) return;

    if (!targets || targets.length === 0) {
      DOM.configTargetsTbody.innerHTML = `<tr><td colspan="4" class="text-center" style="color: var(--text-muted); padding: 1.5rem;">No probe targets defined.</td></tr>`;
      return;
    }

    DOM.configTargetsTbody.innerHTML = targets.map((t, idx) => {
      const name = escapeHTML(t.name || t.host);
      const host = escapeHTML(t.host);
      const cat = escapeHTML(t.category || 'WAN');

      let catBadgeClass = 'badge-neutral';
      if (cat.includes('LAN')) catBadgeClass = 'badge-blue';
      else if (cat.includes('ANYCAST')) catBadgeClass = 'badge-purple';
      else if (cat.includes('GAME')) catBadgeClass = 'badge-yellow';

      return `
        <tr>
          <td><span style="font-weight: 500; color: #f4f4f5;">${name}</span></td>
          <td><span class="mono" style="color: #38bdf8;">${host}</span></td>
          <td><span class="badge-pill mono ${catBadgeClass}">${cat}</span></td>
          <td style="text-align: right;">
            <button type="button" class="btn-table-del" data-action="del-target" data-index="${idx}" title="Remove Target">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"></polyline>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
              </svg>
            </button>
          </td>
        </tr>
      `;
    }).join('');

    DOM.configTargetsTbody.querySelectorAll('button[data-action="del-target"]').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        const idx = Number(btn.dataset.index);
        if (state.workingConfig && state.workingConfig.targets) {
          if (state.workingConfig.targets.length <= 1) {
            showToast('At least one probe target must remain in configuration.', 'error');
            return;
          }
          const removed = state.workingConfig.targets.splice(idx, 1)[0];
          renderConfigTargetsTable(state.workingConfig.targets);
          // Persist removal and immediately reload status and charts
          await handleSaveConfiguration(false);
          showToast(`Target ${removed.name || removed.host} removed and applied`, 'success');
        }
      });
    });
  }

  function handleAddTarget() {
    const name = DOM.newTargetName ? DOM.newTargetName.value.trim() : '';
    const host = DOM.newTargetHost ? DOM.newTargetHost.value.trim() : '';
    const category = DOM.newTargetCategory ? DOM.newTargetCategory.value : 'Public Anycast';

    if (!host) {
      showToast('Host / IP address is required.', 'error');
      if (DOM.newTargetHost) DOM.newTargetHost.focus();
      return;
    }

    if (!state.workingConfig) {
      state.workingConfig = { targets: [] };
    }
    if (!state.workingConfig.targets) {
      state.workingConfig.targets = [];
    }

    state.workingConfig.targets.push({
      name: name || host,
      host: host,
      category: category
    });

    renderConfigTargetsTable(state.workingConfig.targets);

    // Clear inputs
    if (DOM.newTargetName) DOM.newTargetName.value = '';
    if (DOM.newTargetHost) DOM.newTargetHost.value = '';
    showToast(`Added target ${host}`, 'success');
  }

  function renderConfigDnsTable(servers) {
    if (!DOM.configDnsTbody) return;

    if (!servers || servers.length === 0) {
      DOM.configDnsTbody.innerHTML = `<tr><td colspan="3" class="text-center" style="color: var(--text-muted); padding: 1.5rem;">No DNS resolvers configured.</td></tr>`;
      return;
    }

    DOM.configDnsTbody.innerHTML = servers.map((srv, idx) => {
      const name = escapeHTML(srv.name || srv.host);
      const host = escapeHTML(srv.host);

      return `
        <tr>
          <td><span style="font-weight: 500; color: #f4f4f5;">${name}</span></td>
          <td><span class="mono" style="color: #a78bfa;">${host}</span></td>
          <td style="text-align: right;">
            <button type="button" class="btn-table-del" data-action="del-dns" data-index="${idx}" title="Remove Resolver">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"></polyline>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
              </svg>
            </button>
          </td>
        </tr>
      `;
    }).join('');

    DOM.configDnsTbody.querySelectorAll('button[data-action="del-dns"]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        const idx = Number(btn.dataset.index);
        if (state.workingConfig && state.workingConfig.dns && state.workingConfig.dns.servers) {
          state.workingConfig.dns.servers.splice(idx, 1);
          renderConfigDnsTable(state.workingConfig.dns.servers);
        }
      });
    });
  }

  function handleAddDnsServer() {
    const name = DOM.newDnsName ? DOM.newDnsName.value.trim() : '';
    let host = DOM.newDnsHost ? DOM.newDnsHost.value.trim() : '';

    if (!host) {
      showToast('DNS server host:port is required.', 'error');
      if (DOM.newDnsHost) DOM.newDnsHost.focus();
      return;
    }

    if (!host.includes(':')) {
      host = `${host}:53`;
    }

    if (!state.workingConfig) state.workingConfig = {};
    if (!state.workingConfig.dns) state.workingConfig.dns = { servers: [] };
    if (!state.workingConfig.dns.servers) state.workingConfig.dns.servers = [];

    state.workingConfig.dns.servers.push({
      name: name || host,
      host: host
    });

    renderConfigDnsTable(state.workingConfig.dns.servers);

    if (DOM.newDnsName) DOM.newDnsName.value = '';
    if (DOM.newDnsHost) DOM.newDnsHost.value = '';
    showToast(`Added DNS resolver ${host}`, 'success');
  }

  async function handleSaveConfiguration(closeDrawer = true) {
    if (!state.workingConfig) return;

    // Validate targets
    const targets = state.workingConfig.targets || [];
    if (targets.length === 0) {
      showToast('Validation Error: At least one ping target must be defined.', 'error');
      return;
    }

    // Build payload
    const payload = {
      server: state.cachedConfig?.server || { port: 7366 },
      database: state.cachedConfig?.database || { path: './data/tenom.db', retention_days: 7 },
      probe: {
        interval_seconds: parseInt(DOM.cfgProbeInterval?.value, 10) || 15,
        packet_count: parseInt(DOM.cfgProbePackets?.value, 10) || 3,
        timeout_seconds: parseInt(DOM.cfgProbeTimeout?.value, 10) || 3,
        jitter_threshold_ms: parseFloat(DOM.cfgJitterThreshold?.value) || 120.0,
        loss_threshold_pct: parseFloat(DOM.cfgLossThreshold?.value) || 10.0
      },
      dns: {
        enabled: DOM.cfgDnsEnabled ? DOM.cfgDnsEnabled.checked : true,
        interval_seconds: parseInt(DOM.cfgDnsInterval?.value, 10) || 30,
        timeout_seconds: state.cachedConfig?.dns?.timeout_seconds || 3,
        domain: DOM.cfgDnsDomain ? DOM.cfgDnsDomain.value.trim() || 'google.com' : 'google.com',
        servers: state.workingConfig.dns?.servers || []
      },
      targets: targets
    };

    const saveBtn = DOM.btnSaveConfig;
    const originalText = saveBtn ? saveBtn.innerHTML : '';
    if (saveBtn) {
      saveBtn.disabled = true;
      saveBtn.innerHTML = '<span class="pulse-indicator"></span> Saving...';
    }

    try {
      const res = await fetch(CONFIG.endpoints.config, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      const result = await res.json();
      if (!res.ok) {
        throw new Error(result.error || `HTTP ${res.status}`);
      }

      state.cachedConfig = payload;
      state.workingConfig = JSON.parse(JSON.stringify(payload));

      if (closeDrawer) {
        showToast('Configuration successfully saved and applied!', 'success');
        closeSettingsDrawer();
      }

      // Trigger immediate metrics refresh & rebuild UI/chart series dynamically
      await fetchStatus();
      await fetchPingMetrics();
      await fetchDnsMetrics();
      await fetchIncidents();
    } catch (err) {
      console.error('[Tenom] Save config error:', err);
      showToast(`Save failed: ${err.message}`, 'error');
    } finally {
      if (saveBtn) {
        saveBtn.disabled = false;
        saveBtn.innerHTML = originalText;
      }
    }
  }

  // --- Toast Notification Helper ---

  function showToast(message, type = 'success', duration = 3800) {
    if (!DOM.toastContainer) return;

    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;

    const iconSvg = type === 'success'
      ? `<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"></path><polyline points="22 4 12 14.01 9 11.01"></polyline></svg>`
      : `<svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>`;

    toast.innerHTML = `
      ${iconSvg}
      <div class="toast-content">${escapeHTML(message)}</div>
    `;

    DOM.toastContainer.appendChild(toast);

    setTimeout(() => {
      toast.classList.add('toast-hiding');
      setTimeout(() => {
        if (toast.parentNode) toast.parentNode.removeChild(toast);
      }, 250);
    }, duration);
  }

  // --- Utilities & Formatters ---

  function renderStatusError(err) {
    DOM.systemStatusBadge.className = 'status-badge status-outage';
    DOM.systemStatusText.textContent = 'Backend Reconnecting...';
    DOM.kpiStateVal.textContent = 'OFFLINE';
    DOM.kpiStateVal.className = 'kpi-value status-text-red';
    DOM.kpiStateDesc.textContent = `Cannot reach daemon on port 7366 (${err.message})`;
  }

  function updateLastSyncTime() {
    if (!DOM.lastUpdatedText) return;
    const now = new Date();
    const timeStr = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    DOM.lastUpdatedText.textContent = `Updated ${timeStr}`;
  }

  function formatDuration(totalSeconds) {
    if (totalSeconds < 60) return `${totalSeconds}s`;
    const m = Math.floor(totalSeconds / 60);
    const s = totalSeconds % 60;
    if (m < 60) return `${m}m ${s}s`;
    const h = Math.floor(m / 60);
    const remM = m % 60;
    return `${h}h ${remM}m`;
  }

  function calculateDurationStr(startedAt, resolvedAt) {
    if (!startedAt) return '--';
    const start = new Date(startedAt).getTime();
    if (!resolvedAt) return 'Ongoing';
    const end = new Date(resolvedAt).getTime();
    const diffSec = Math.max(0, Math.round((end - start) / 1000));
    return formatDuration(diffSec);
  }

  function formatDateTime(isoString) {
    if (!isoString) return '--';
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return isoString;
    return d.toLocaleString([], {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false
    });
  }

  function formatTimeOnly(isoString) {
    if (!isoString) return '--';
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return isoString;
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
  }

  function formatTimestampKey(isoString) {
    if (!isoString) return '';
    const d = new Date(isoString);
    return isNaN(d.getTime()) ? isoString : d.toISOString();
  }

  function formatTimestampLabel(isoString) {
    if (!isoString) return '';
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return isoString;
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
  }

  function formatChartXAxisLabel(val) {
    if (!val) return '';
    if (val.length >= 5 && val.includes(':')) {
      return val.substring(0, 5);
    }
    return val;
  }

  function getRelativeTime(isoString) {
    if (!isoString) return '';
    const d = new Date(isoString);
    const diffSec = Math.round((Date.now() - d.getTime()) / 1000);
    if (diffSec < 60) return `${diffSec}s ago`;
    const min = Math.floor(diffSec / 60);
    if (min < 60) return `${min}m ago`;
    const hr = Math.floor(min / 60);
    if (hr < 24) return `${hr}h ago`;
    const days = Math.floor(hr / 24);
    return `${days}d ago`;
  }

  function hexToRgba(hex, alpha) {
    let c;
    if (/^#([A-Fa-f0-9]{3}){1,2}$/.test(hex)) {
      c = hex.substring(1).split('');
      if (c.length === 3) {
        c = [c[0], c[0], c[1], c[1], c[2], c[2]];
      }
      c = '0x' + c.join('');
      return `rgba(${[(c >> 16) & 255, (c >> 8) & 255, c & 255].join(',')},${alpha})`;
    }
    return hex;
  }

  function escapeHTML(str) {
    if (typeof str !== 'string') return str;
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  // --- Startup ---
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
