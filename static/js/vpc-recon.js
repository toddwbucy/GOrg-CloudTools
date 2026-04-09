'use strict';

// Module-level state
let reportMarkdown = '';
let lastSummary = null;

const COM_REGIONS = [
    'us-east-1', 'us-east-2', 'us-west-1', 'us-west-2',
    'eu-west-1', 'eu-west-2', 'eu-central-1',
    'ap-southeast-1', 'ap-southeast-2', 'ap-northeast-1',
];
const GOV_REGIONS = ['us-gov-east-1', 'us-gov-west-1'];

// Safe Bootstrap color tokens (never user-supplied)
const SAFE_COLORS = new Set([
    'primary', 'secondary', 'success', 'danger', 'warning', 'info', 'light', 'dark'
]);

// ── Bootstrap region select when environment changes ──────────────────────────
function onEnvironmentChange() {
    const env = document.getElementById('environment').value;
    const regionSel = document.getElementById('region');
    const regions = env === 'gov' ? GOV_REGIONS : COM_REGIONS;

    regionSel.replaceChildren();
    regions.forEach(r => {
        const opt = document.createElement('option');
        opt.value = r;
        opt.textContent = r;
        regionSel.appendChild(opt);
    });
}

// ── Run scan ──────────────────────────────────────────────────────────────────
async function runScan() {
    const vpcId     = (document.getElementById('vpc-id').value || '').trim();
    const region    = (document.getElementById('region').value || '').trim();
    const accountId = (document.getElementById('account-id').value || '').trim();
    const env       = (document.getElementById('environment').value || 'com').trim();

    // Client-side validation
    const errors = [];
    if (!vpcId.startsWith('vpc-'))    errors.push('VPC ID must start with "vpc-"');
    if (!region)                       errors.push('Region is required');
    if (!/^\d{12}$/.test(accountId))   errors.push('Account ID must be exactly 12 digits');
    if (errors.length) {
        showError(errors.join(' | '));
        return;
    }

    hideError();
    showProgress('Scanning VPC — this may take 15–30 seconds…');
    setInputsDisabled(true);

    try {
        const headers = window.Utils
            ? window.Utils.buildCsrfHeaders({ 'Content-Type': 'application/json' })
            : { 'Content-Type': 'application/json' };

        const resp = await fetch('/aws/vpc-recon/scan', {
            method: 'POST',
            headers,
            body: JSON.stringify({
                vpc_id: vpcId,
                region,
                account_id: accountId,
                environment: env,
            }),
        });

        const json = await resp.json();

        if (!resp.ok) {
            showError(json.error || `HTTP ${resp.status}`);
            return;
        }

        reportMarkdown = json.markdown || '';
        lastSummary = json.summary || {};

        renderSummaryCards(lastSummary);
        renderReport(reportMarkdown);
        document.getElementById('results-section').style.display = '';

    } catch (err) {
        showError('Network error: ' + err.message);
    } finally {
        hideProgress();
        setInputsDisabled(false);
    }
}

// ── Render summary cards using DOM methods (no innerHTML) ─────────────────────
function renderSummaryCards(summary) {
    const counts = summary.counts || {};

    const cardData = [
        { icon: 'bi-hdd-network',         label: 'Subnets',          value: counts.subnets           ?? '-', color: 'primary'   },
        { icon: 'bi-ethernet',            label: 'ENIs (total)',      value: counts.enis              ?? '-', color: 'secondary' },
        { icon: 'bi-x-circle',            label: 'Orphaned ENIs',     value: counts.enis_orphaned     ?? '-', color: counts.enis_orphaned > 0 ? 'warning' : 'success' },
        { icon: 'bi-exclamation-triangle',label: 'Primary NICs',      value: counts.enis_primary      ?? '-', color: counts.enis_primary > 0 ? 'danger' : 'success' },
        { icon: 'bi-pc-display',          label: 'EC2 Instances',     value: counts.instances         ?? '-', color: 'info'      },
        { icon: 'bi-database',            label: 'RDS Instances',     value: counts.rds               ?? '-', color: 'info'      },
        { icon: 'bi-distribute-vertical', label: 'Load Balancers',    value: counts.load_balancers    ?? '-', color: 'info'      },
        { icon: 'bi-shield-check',        label: 'Security Groups',   value: counts.security_groups   ?? '-', color: 'secondary' },
        { icon: 'bi-arrow-left-right',    label: 'NAT Gateways',      value: counts.nat_gateways      ?? '-', color: 'secondary' },
        { icon: 'bi-activity',            label: 'Flow Logs',         value: counts.flow_logs         ?? '-', color: counts.flow_logs > 0 ? 'success' : 'warning' },
    ];

    const container = document.getElementById('summary-cards');
    container.replaceChildren();

    cardData.forEach(c => {
        const color = SAFE_COLORS.has(c.color) ? c.color : 'secondary';

        const col = document.createElement('div');
        col.className = 'col-6 col-md-3 col-xl-2';

        const card = document.createElement('div');
        card.className = 'card text-center border-' + color;

        const body = document.createElement('div');
        body.className = 'card-body py-2';

        const icon = document.createElement('i');
        icon.className = 'bi ' + c.icon + ' fs-4 text-' + color;

        const valDiv = document.createElement('div');
        valDiv.className = 'fw-bold fs-4';
        valDiv.textContent = String(c.value);

        const label = document.createElement('small');
        label.className = 'text-muted';
        label.textContent = c.label;

        body.append(icon, valDiv, label);
        card.appendChild(body);
        col.appendChild(card);
        container.appendChild(col);
    });
}

// ── Render raw markdown in <pre> ──────────────────────────────────────────────
function renderReport(markdown) {
    // textContent prevents any XSS — raw markdown is displayed as plain text
    document.getElementById('report-content').textContent = markdown;
}

// ── Download as .md file ──────────────────────────────────────────────────────
function downloadReport() {
    if (!reportMarkdown) return;

    const vpcId = (document.getElementById('vpc-id').value || 'vpc-recon').trim();
    const ts    = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const filename = 'vpc-recon_' + vpcId + '_' + ts + '.md';

    const blob = new Blob([reportMarkdown], { type: 'text/markdown; charset=utf-8' });
    const url  = URL.createObjectURL(blob);
    const a    = document.createElement('a');
    a.href     = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
}

// ── UI helpers ────────────────────────────────────────────────────────────────
function showProgress(msg) {
    document.getElementById('scan-progress').style.display = '';
    document.getElementById('scan-progress-text').textContent = msg;
    document.getElementById('scan-btn').disabled = true;
}

function hideProgress() {
    document.getElementById('scan-progress').style.display = 'none';
    document.getElementById('scan-btn').disabled = false;
}

function showError(msg) {
    const el = document.getElementById('scan-error');
    el.textContent = msg;
    el.style.display = '';
}

function hideError() {
    document.getElementById('scan-error').style.display = 'none';
}

function setInputsDisabled(disabled) {
    ['vpc-id', 'region', 'account-id', 'environment'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.disabled = disabled;
    });
}
