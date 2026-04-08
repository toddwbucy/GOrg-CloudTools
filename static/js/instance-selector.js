/**
 * InstanceSelector — reusable instance selection component.
 *
 * Security: All dynamic content inserted via innerHTML is escaped through the
 * local `escape` helper (which delegates to window.Utils.escapeHtml when available)
 * before being placed in the DOM. No raw user input is ever interpolated directly.
 *
 * Usage:
 *   const selector = new InstanceSelector('my-container-id');
 *   selector.setInstances(instanceArray);
 *   selector.onSelectionChange(selectedInstances => { ... });
 *   const selected = selector.getSelected();
 *
 * The container element fires a 'selection-changed' DOM event (bubbles) on every
 * change, carrying { detail: { selected: [...], count: N } } for external listeners.
 */
(function () {
    'use strict';

    // XSS-safe escape helper — delegates to Utils when loaded, self-contained fallback otherwise
    function esc(value) {
        const s = String(value ?? '');
        if (window.Utils && window.Utils.escapeHtml) return window.Utils.escapeHtml(s);
        return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    class InstanceSelector {
        constructor(containerId) {
            this.container = document.getElementById(containerId);
            if (!this.container) {
                console.warn(`InstanceSelector: container #${containerId} not found`);
            }
            this._instances = [];
            this._selected = new Set();
            this._callbacks = [];
            this._platformFilter = '';
            this._regionFilter = '';
            this._render();
        }

        // ── Public API ──────────────────────────────────────────────────────

        /** Replace the instance list (called when a change is loaded). Selects all by default. */
        setInstances(instances) {
            this._instances = instances || [];
            this._selected = new Set(this._instances.map(i => i.instance_id));
            this._platformFilter = '';
            this._regionFilter = '';
            this._render();
            this._fireChange();
        }

        /** Returns all currently selected instance objects. */
        getSelected() {
            return this._instances.filter(i => this._selected.has(i.instance_id));
        }

        /** Returns just the selected instance_id strings. */
        getSelectedIds() {
            return Array.from(this._selected);
        }

        /** Register a callback invoked with the selected instances array on every change. */
        onSelectionChange(callback) {
            this._callbacks.push(callback);
        }

        selectAll() {
            this._visibleInstances().forEach(i => this._selected.add(i.instance_id));
            this._render();
            this._fireChange();
        }

        deselectAll() {
            this._visibleInstances().forEach(i => this._selected.delete(i.instance_id));
            this._render();
            this._fireChange();
        }

        // ── Private ──────────────────────────────────────────────────────────

        _visibleInstances() {
            return this._instances.filter(i => {
                if (this._platformFilter && i.platform !== this._platformFilter) return false;
                if (this._regionFilter && i.region !== this._regionFilter) return false;
                return true;
            });
        }

        _fireChange() {
            const selected = this.getSelected();
            this._callbacks.forEach(cb => cb(selected));
            if (this.container) {
                this.container.dispatchEvent(new CustomEvent('selection-changed', {
                    detail: { selected, count: selected.length },
                    bubbles: true,
                }));
            }
        }

        _toggle(instanceId) {
            if (this._selected.has(instanceId)) {
                this._selected.delete(instanceId);
            } else {
                this._selected.add(instanceId);
            }
            this._render();
            this._fireChange();
        }

        _uniqueValues(field) {
            return [...new Set(this._instances.map(i => i[field]).filter(Boolean))].sort();
        }

        _buildHtml() {
            const total = this._instances.length;
            const visible = this._visibleInstances();
            const selectedCount = this.getSelected().length;
            const visibleSelectedCount = visible.filter(i => this._selected.has(i.instance_id)).length;
            const allVisibleSelected = visible.length > 0 && visibleSelectedCount === visible.length;
            const someVisibleSelected = visibleSelectedCount > 0 && visibleSelectedCount < visible.length;

            const platforms = this._uniqueValues('platform');
            const regions = this._uniqueValues('region');
            const cid = esc(this.container.id);

            const platformOptions = platforms.map(p =>
                `<option value="${esc(p)}" ${this._platformFilter === p ? 'selected' : ''}>${esc(p)}</option>`
            ).join('');

            const regionOptions = regions.map(r =>
                `<option value="${esc(r)}" ${this._regionFilter === r ? 'selected' : ''}>${esc(r)}</option>`
            ).join('');

            const rows = visible.map(inst => {
                const checked = this._selected.has(inst.instance_id) ? 'checked' : '';
                const rowClass = this._selected.has(inst.instance_id) ? '' : 'table-secondary text-muted';
                const platformBadge = inst.platform === 'linux' ? 'bg-success' : 'bg-primary';
                // instance_id comes from DB — escape for DOM safety
                return `
                    <tr class="${rowClass}">
                        <td class="text-center">
                            <input type="checkbox" class="form-check-input instance-cb"
                                   data-id="${esc(inst.instance_id)}" ${checked}>
                        </td>
                        <td><code class="small">${esc(inst.instance_id)}</code></td>
                        <td><small>${esc(inst.account_id || '-')}</small></td>
                        <td><small>${esc(inst.region || '-')}</small></td>
                        <td><span class="badge ${platformBadge}">${esc(inst.platform || '-')}</span></td>
                        <td><small class="text-muted">${esc(inst.name || '-')}</small></td>
                    </tr>`;
            }).join('');

            let noDataMsg = '';
            if (total === 0) {
                noDataMsg = '<div class="alert alert-warning mb-0 small"><i class="bi bi-info-circle me-1"></i>No change loaded. Load a change to select instances.</div>';
            } else if (visible.length === 0) {
                noDataMsg = '<div class="alert alert-info mb-0 small">No instances match the current filter.</div>';
            }

            return `
                <div class="d-flex flex-wrap gap-2 align-items-center mb-2">
                    <button type="button" class="btn btn-sm btn-outline-primary" id="${cid}-btn-all">
                        <i class="bi bi-check-all me-1"></i>Select All
                    </button>
                    <button type="button" class="btn btn-sm btn-outline-secondary" id="${cid}-btn-none">
                        <i class="bi bi-x-square me-1"></i>Deselect All
                    </button>
                    ${platforms.length > 1 ? `
                        <select class="form-select form-select-sm" style="width:auto;" id="${cid}-plat">
                            <option value="">All Platforms</option>${platformOptions}
                        </select>` : ''}
                    ${regions.length > 1 ? `
                        <select class="form-select form-select-sm" style="width:auto;" id="${cid}-reg">
                            <option value="">All Regions</option>${regionOptions}
                        </select>` : ''}
                    <span class="ms-auto badge bg-primary fs-6" id="${cid}-count">
                        ${selectedCount} / ${total} selected
                    </span>
                </div>
                ${noDataMsg}
                ${visible.length > 0 ? `
                <div style="max-height:300px; overflow-y:auto;">
                    <table class="table table-sm table-hover mb-0">
                        <thead class="table-light" style="position:sticky;top:0;z-index:1;">
                            <tr>
                                <th style="width:40px;" class="text-center">
                                    <input type="checkbox" class="form-check-input" id="${cid}-hdr-cb"
                                           ${allVisibleSelected ? 'checked' : ''}>
                                </th>
                                <th>Instance ID</th>
                                <th>Account</th>
                                <th>Region</th>
                                <th>Platform</th>
                                <th>Name</th>
                            </tr>
                        </thead>
                        <tbody>${rows}</tbody>
                    </table>
                </div>` : ''}`;
        }

        _render() {
            if (!this.container) return;
            // All content is built via _buildHtml() using the esc() helper on every
            // dynamic value — no raw user/server strings are interpolated unsanitized.
            this.container.innerHTML = this._buildHtml(); // safe: all values are escaped via esc()
            this._attachEvents();
        }

        _attachEvents() {
            if (!this.container) return;
            const cid = this.container.id;

            const get = id => this.container.querySelector(`#${id}`);

            const btnAll = get(`${cid}-btn-all`);
            if (btnAll) btnAll.addEventListener('click', () => this.selectAll());

            const btnNone = get(`${cid}-btn-none`);
            if (btnNone) btnNone.addEventListener('click', () => this.deselectAll());

            const hdrCb = get(`${cid}-hdr-cb`);
            if (hdrCb) {
                const visible = this._visibleInstances();
                const sel = visible.filter(i => this._selected.has(i.instance_id)).length;
                hdrCb.indeterminate = sel > 0 && sel < visible.length;
                hdrCb.addEventListener('change', () => {
                    if (hdrCb.checked) this.selectAll(); else this.deselectAll();
                });
            }

            this.container.querySelectorAll('.instance-cb').forEach(cb => {
                cb.addEventListener('change', () => this._toggle(cb.dataset.id));
            });

            const platSel = get(`${cid}-plat`);
            if (platSel) platSel.addEventListener('change', () => {
                this._platformFilter = platSel.value;
                this._render();
            });

            const regSel = get(`${cid}-reg`);
            if (regSel) regSel.addEventListener('change', () => {
                this._regionFilter = regSel.value;
                this._render();
            });
        }
    }

    window.InstanceSelector = InstanceSelector;

})();
