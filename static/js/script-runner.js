// AWS Script Runner JavaScript
(function() {
    'use strict';

    // Module-scoped variables
    let currentChange = null;
    let currentBatchId = null;
    let executionPolling = null;
    let instanceSelector = null;

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
    checkCredentials();

    // Initialize the instance selector
    instanceSelector = new InstanceSelector('instance-selector-body');
    instanceSelector.onSelectionChange(function(selected) {
        updateExecuteButton();
    });

    if (window.currentChangeData) {
        currentChange = window.currentChangeData;
        instanceSelector.setInstances(currentChange.instances || []);
        updateExecuteButton();
    }

    loadLibrary();

    const scriptContent = document.getElementById('script-content');
    if (scriptContent) {
        scriptContent.addEventListener('input', updateExecuteButton);
    }
});

// =============================================================================
// Credentials Management
// =============================================================================

async function checkCredentials() {
    // Credentials are stored in session by /aws/authenticate
    // Status badges are updated by template, just for logging
    const comStatus = document.getElementById('com-creds-status');
    const govStatus = document.getElementById('gov-creds-status');

    if (comStatus && govStatus) {
        console.log('Credential status badges found - user can set credentials via AWS page');
    }
}

// =============================================================================
// Execute Button Management
// =============================================================================

function updateExecuteButton() {
    const executeBtn = document.getElementById('execute-btn');
    const executeStatus = document.getElementById('execute-status');
    const scriptContent = document.getElementById('script-content');

    if (!executeBtn || !executeStatus || !scriptContent) return;

    const hasScript = scriptContent.value.trim().length > 0;
    const selectedInstances = instanceSelector ? instanceSelector.getSelected() : [];
    const hasInstances = selectedInstances.length > 0;

    if (hasScript && hasInstances) {
        executeBtn.disabled = false;
        executeStatus.textContent = `Ready to execute on ${selectedInstances.length} instance(s)`;
        executeStatus.className = 'text-success small';
    } else if (!hasScript) {
        executeBtn.disabled = true;
        executeStatus.textContent = 'Enter script content to execute';
        executeStatus.className = 'text-muted small';
    } else if (!hasInstances) {
        executeBtn.disabled = true;
        executeStatus.textContent = 'No instances selected — select at least one above';
        executeStatus.className = 'text-warning small';
    }
}

// =============================================================================
// Connectivity Test
// =============================================================================

async function testConnectivity() {
    const selectedInstances = instanceSelector ? instanceSelector.getSelected() : [];
    if (selectedInstances.length === 0) {
        showToast('No instances selected', 'warning');
        return;
    }

    const resultsDiv = document.getElementById('connectivity-results');
    const testBtn = document.getElementById('test-connectivity-btn');

    // Disable button and show loading
    testBtn.disabled = true;
    testBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Confirming...';
    resultsDiv.className = 'alert alert-info';
    resultsDiv.innerHTML = '<i class="bi bi-hourglass-split me-1"></i>Confirming connectivity to all instances...';

    try {
        const response = await fetch('/aws/script-runner/test-connectivity', {
            method: 'POST',
            headers: Object.assign(
                {'Content-Type': 'application/json'},
                (window.Utils && window.Utils.getCsrfToken()) ? {'X-CSRFToken': window.Utils.getCsrfToken()} : {}
            ),
            body: JSON.stringify({
                instance_ids: selectedInstances.map(i => i.instance_id)
            }),
            credentials: 'same-origin'
        });

        if (response.ok) {
            const data = await response.json();
            const results = data.results || [];

            // Count accessible instances (array of {instance_id, accessible, error})
            const accessibleCount = results.filter(r => r.accessible).length;
            const totalCount = results.length;

            // Determine alert class based on results
            let alertClass = 'alert-success';
            let iconClass = 'bi-check-circle';
            if (accessibleCount === 0 && totalCount > 0) {
                alertClass = 'alert-danger';
                iconClass = 'bi-x-circle';
            } else if (accessibleCount < totalCount) {
                alertClass = 'alert-warning';
                iconClass = 'bi-exclamation-triangle';
            }

            // Build results HTML with escaped content
            let html = `<div class="mb-2">
                <i class="bi ${iconClass} me-1"></i>
                <strong>${accessibleCount} of ${totalCount} instances accessible</strong>
            </div>`;

            // Add instance details (all user content is escaped)
            if (results.length > 0) {
                html += '<div class="small">';
                results.forEach(result => {
                    const icon = result.accessible ? '✅' : '❌';
                    const escapedId = window.Utils.escapeHtml(result.instance_id);
                    const escapedError = result.error ? window.Utils.escapeHtml(result.error) : 'Not accessible';
                    const statusText = result.accessible ? 'Accessible' : escapedError;
                    html += `<div>${icon} ${escapedId}: ${statusText}</div>`;
                });
                html += '</div>';
            }

            resultsDiv.className = `alert ${alertClass}`;
            resultsDiv.innerHTML = html;
        } else {
            const errorData = await response.json().catch(() => ({ detail: 'Unknown error' }));
            resultsDiv.className = 'alert alert-danger';
            resultsDiv.innerHTML = `
                <i class="bi bi-x-circle me-1"></i>
                <strong>Error:</strong> ${window.Utils.escapeHtml(errorData.detail || 'Connectivity test failed')}
            `;
        }
    } catch (error) {
        console.error('Connectivity test error:', error);
        resultsDiv.className = 'alert alert-danger';
        resultsDiv.innerHTML = `
            <i class="bi bi-x-circle me-1"></i>
            <strong>Error:</strong> ${window.Utils.escapeHtml(error.message)}
        `;
    } finally {
        // Re-enable button
        testBtn.disabled = false;
        testBtn.innerHTML = '<i class="bi bi-wifi me-2"></i>Confirm Connectivity';
    }
}

// =============================================================================
// Script Validation
// =============================================================================

async function validateScript() {
    const scriptContent = document.getElementById('script-content').value;
    const interpreter = document.getElementById('interpreter').value;
    const resultsDiv = document.getElementById('validation-results');

    if (!scriptContent.trim()) {
        resultsDiv.innerHTML = `
            <div class="alert alert-warning">
                <i class="bi bi-exclamation-triangle me-1"></i>Please enter script content first
            </div>
        `;
        return;
    }

    try {
        const response = await fetch('/aws/script-runner/validate-script', {
            method: 'POST',
            headers: Object.assign(
                {'Content-Type': 'application/json'},
                (window.Utils && window.Utils.getCsrfToken()) ? {'X-CSRFToken': window.Utils.getCsrfToken()} : {}
            ),
            body: JSON.stringify({
                content: scriptContent,
                interpreter: interpreter
            }),
            credentials: 'same-origin'
        });

        if (response.ok) {
            const data = await response.json();

            if (data.warnings && data.warnings.length > 0) {
                const warningsList = data.warnings.map(w => `<li>${window.Utils.escapeHtml(w)}</li>`).join('');
                resultsDiv.innerHTML = `
                    <div class="alert alert-warning">
                        <i class="bi bi-exclamation-triangle me-1"></i>
                        <strong>Validation Warnings:</strong>
                        <ul class="mb-0 mt-2">${warningsList}</ul>
                    </div>
                `;
            } else {
                resultsDiv.innerHTML = `
                    <div class="alert alert-success">
                        <i class="bi bi-check-circle me-1"></i>No dangerous patterns detected
                    </div>
                `;
            }
        } else {
            const errorData = await response.json().catch(() => ({ detail: 'Validation failed' }));
            resultsDiv.innerHTML = `
                <div class="alert alert-danger">
                    <i class="bi bi-x-circle me-1"></i>${window.Utils.escapeHtml(errorData.detail)}
                </div>
            `;
        }
    } catch (error) {
        console.error('Validation error:', error);
        resultsDiv.innerHTML = `
            <div class="alert alert-danger">
                <i class="bi bi-x-circle me-1"></i>Error: ${window.Utils.escapeHtml(error.message)}
            </div>
        `;
    }
}

// =============================================================================
// Script Execution
// =============================================================================

async function executeScript() {
    const scriptName = document.getElementById('script-name').value || 'Untitled Script';
    const scriptContent = document.getElementById('script-content').value;
    const interpreter = document.getElementById('interpreter').value;
    const description = document.getElementById('script-description').value;
    const saveToLibrary = document.getElementById('save-to-library').checked;

    if (!scriptContent.trim()) {
        showToast('Please enter script content', 'warning');
        return;
    }

    const selectedInstances = instanceSelector ? instanceSelector.getSelected() : [];
    if (selectedInstances.length === 0) {
        showToast('No instances selected — select at least one instance above', 'warning');
        return;
    }

    // Disable execute button
    const executeBtn = document.getElementById('execute-btn');
    executeBtn.disabled = true;
    executeBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Starting...';

    try {
        const response = await fetch('/aws/script-runner/execute', {
            method: 'POST',
            headers: Object.assign(
                {'Content-Type': 'application/json'},
                (window.Utils && window.Utils.getCsrfToken()) ? {'X-CSRFToken': window.Utils.getCsrfToken()} : {}
            ),
            body: JSON.stringify({
                name: scriptName,
                content: scriptContent,
                interpreter: interpreter,
                description: description,
                save_to_library: saveToLibrary,
                instance_ids: selectedInstances.map(i => i.instance_id)
            }),
            credentials: 'same-origin'
        });

        if (response.ok) {
            const data = await response.json();
            currentBatchId = data.batch_id;

            showToast(`Script execution started on ${data.execution_count} instance(s)`, 'success');

            // Start polling for results
            pollResults(data.batch_id);
        } else {
            const errorData = await response.json().catch(() => ({ detail: 'Execution failed' }));
            showToast(`Error: ${errorData.detail}`, 'danger');
            executeBtn.disabled = false;
            executeBtn.innerHTML = '<i class="bi bi-play-circle me-2"></i>Execute Script';
        }
    } catch (error) {
        console.error('Execution error:', error);
        showToast(`Error: ${error.message}`, 'danger');
        executeBtn.disabled = false;
        executeBtn.innerHTML = '<i class="bi bi-play-circle me-2"></i>Execute Script';
    }
}

// =============================================================================
// Results Polling
// =============================================================================

function pollResults(batchId) {
    // Stop any existing polling
    if (executionPolling && executionPolling.stop) {
        executionPolling.stop();
        executionPolling = null;
    }

    // Create polling with exponential backoff
    const polling = window.Utils.createPolling(async (pollCount) => {
        try {
            const response = await fetch(`/aws/script-runner/results/${batchId}`, {
                credentials: 'same-origin'
            });

            if (response.ok) {
                const data = await response.json();

                // Update results display
                displayResults(data);

                // Check if completed
                const totalPending = (data.status_counts.pending || 0) + (data.status_counts.running || 0);
                if (totalPending === 0) {
                    const hasFailed = (data.status_counts.failed || 0) > 0;
                    showExecutionStatus(
                        hasFailed ? 'Execution completed with errors' : 'Execution completed',
                        hasFailed ? 'warning' : 'success'
                    );
                    showToast('Script execution completed', hasFailed ? 'warning' : 'success');

                    // Re-enable execute button
                    const executeBtn = document.getElementById('execute-btn');
                    executeBtn.disabled = false;
                    executeBtn.innerHTML = '<i class="bi bi-play-circle me-2"></i>Execute Script';

                    // Show download section
                    const downloadSection = document.getElementById('download-section');
                    if (downloadSection) {
                        downloadSection.style.display = 'block';
                    }

                    // Stop polling
                    return { continue: false };
                }
            } else {
                console.error('Error fetching results:', response.statusText);
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }
        } catch (error) {
            console.error('Polling error:', error);
            throw error;
        }
    }, {
        initialInterval: 3000,   // Start at 3 seconds
        maxInterval: 15000,      // Max 15 seconds
        backoffMultiplier: 1.5,
        maxPolls: 120,           // 10 minutes max
        onMaxPollsReached: () => {
            showExecutionStatus('Execution timed out after 10 minutes', 'warning');
            showToast('Execution monitoring timed out', 'warning');
            const executeBtn = document.getElementById('execute-btn');
            executeBtn.disabled = false;
            executeBtn.innerHTML = '<i class="bi bi-play-circle me-2"></i>Execute Script';
        },
        onError: (error) => {
            showExecutionStatus('Failed to fetch execution status', 'danger');
            showToast('Lost connection to execution monitor', 'danger');
        }
    });

    // Store reference and start polling
    executionPolling = polling;
    polling.start();
}

function displayResults(data) {
    const statusDiv = document.getElementById('execution-status');
    const resultsDiv = document.getElementById('execution-results');

    const total = data.results.length;
    const completedCount = (data.status_counts.completed || 0) + (data.status_counts.failed || 0);
    const allCompleted = completedCount === total && total > 0;
    const progressPercent = total > 0 ? Math.round((completedCount / total) * 100) : 0;

    // Progress header — matches QC tool style
    const alertClass = allCompleted ? 'alert-success' : 'alert-info';
    const statusIcon = allCompleted
        ? '<i class="bi bi-check-circle-fill me-2"></i>'
        : '<i class="bi bi-hourglass-split me-2"></i>';

    statusDiv.innerHTML = `
        <div class="alert ${alertClass}">
            ${statusIcon}
            <strong>Progress:</strong> ${completedCount} / ${total} instances completed (${progressPercent}%)
            ${!allCompleted ? '<div class="mt-1"><small class="text-muted">Please wait for all executions to complete...</small></div>' : ''}
            ${allCompleted ? '<div class="mt-1"><strong>✓ All instances completed - Ready to download!</strong></div>' : ''}
        </div>
    `;

    // Build results table — uses <details> like the QC tool so output survives DOM rebuilds
    const rows = data.results.map(result => {
        const statusClass = result.status === 'completed' ? 'text-success'
                          : result.status === 'failed'    ? 'text-danger'
                          : result.status === 'running'   ? 'text-primary'
                          : 'text-muted';

        const exitCode = result.exit_code !== null && result.exit_code !== undefined ? result.exit_code : '-';
        const exitCodeClass = result.exit_code === 0 ? 'text-success'
                            : result.exit_code !== null && result.exit_code !== undefined ? 'text-danger' : '';

        let outputHtml = '<span class="text-muted">No output yet</span>';
        if (result.status !== 'pending' && result.stdout) {
            const stderr = result.stderr
                ? `<div class="mt-2"><strong class="text-danger">STDERR:</strong><pre class="bg-danger text-white p-2 rounded small" style="max-height:200px;overflow-y:auto;">${window.Utils.escapeHtml(result.stderr)}</pre></div>`
                : '';
            outputHtml = `
                <details>
                    <summary class="text-primary" style="cursor:pointer;">View Output</summary>
                    <pre class="bg-dark text-white p-2 rounded small mt-1" style="max-height:300px;overflow-y:auto;">${window.Utils.escapeHtml(result.stdout)}</pre>
                    ${stderr}
                </details>`;
        } else if (result.status !== 'pending' && result.stderr) {
            outputHtml = `
                <details>
                    <summary class="text-danger" style="cursor:pointer;">View Error</summary>
                    <pre class="bg-danger text-white p-2 rounded small mt-1" style="max-height:300px;overflow-y:auto;">${window.Utils.escapeHtml(result.stderr)}</pre>
                </details>`;
        }

        return `
            <tr>
                <td><code>${window.Utils.escapeHtml(result.instance_id)}</code></td>
                <td><small>${window.Utils.escapeHtml(result.account_id || '-')}</small></td>
                <td><small>${window.Utils.escapeHtml(result.region || '-')}</small></td>
                <td class="${statusClass} fw-bold">${window.Utils.escapeHtml(result.status)}</td>
                <td class="${exitCodeClass}">${exitCode}</td>
                <td>${outputHtml}</td>
            </tr>
        `;
    }).join('');

    resultsDiv.innerHTML = `
        <div class="table-responsive">
            <table class="table table-sm table-striped table-hover">
                <thead>
                    <tr>
                        <th>Instance ID</th>
                        <th>Account</th>
                        <th>Region</th>
                        <th>Status</th>
                        <th>Exit Code</th>
                        <th>Output</th>
                    </tr>
                </thead>
                <tbody>
                    ${rows}
                </tbody>
            </table>
        </div>
    `;
}

function getStatusBadge(status) {
    const badges = {
        'pending': '<span class="badge bg-secondary">Pending</span>',
        'running': '<span class="badge bg-primary">Running</span>',
        'completed': '<span class="badge bg-success">Completed</span>',
        'failed': '<span class="badge bg-danger">Failed</span>'
    };
    return badges[status] || '<span class="badge bg-secondary">Unknown</span>';
}

function showExecutionStatus(message, type) {
    const statusDiv = document.getElementById('execution-status');
    const alertClass = type === 'success' ? 'alert-success' :
                      type === 'warning' ? 'alert-warning' :
                      type === 'danger' ? 'alert-danger' : 'alert-info';

    statusDiv.innerHTML = `
        <div class="alert ${alertClass}">
            <i class="bi bi-check-circle me-1"></i>${window.Utils.escapeHtml(message)}
        </div>
    `;
}

// =============================================================================
// Download Results
// =============================================================================

function downloadResults(format) {
    if (!currentBatchId) {
        showToast('No results to download', 'warning');
        return;
    }

    const url = `/aws/script-runner/download-results/${currentBatchId}?format=${format}`;
    window.location.href = url;
}

// =============================================================================
// Script Library
// =============================================================================

async function loadLibrary() {
    try {
        const response = await fetch('/aws/script-runner/library', {
            credentials: 'same-origin'
        });

        if (response.ok) {
            const data = await response.json();
            const scripts = data.scripts || [];

            if (scripts.length > 0) {
                const librarySection = document.getElementById('library-section');
                const librarySelect = document.getElementById('library-select');

                // Clear and populate dropdown
                librarySelect.innerHTML = '<option value="">-- Select saved script --</option>';
                scripts.forEach(script => {
                    const option = document.createElement('option');
                    option.value = script.id;
                    option.textContent = `${script.name} (${script.interpreter})`;
                    librarySelect.appendChild(option);
                });

                // Show library section
                if (librarySection) {
                    librarySection.style.display = 'block';
                }
            }
        }
    } catch (error) {
        console.error('Error loading library:', error);
    }
}

async function loadFromLibrary() {
    const librarySelect = document.getElementById('library-select');
    const scriptId = librarySelect.value;

    if (!scriptId) {
        showToast('Please select a script from the library', 'warning');
        return;
    }

    try {
        const response = await fetch(`/aws/script-runner/library/${scriptId}`, {
            credentials: 'same-origin'
        });

        if (response.ok) {
            const script = await response.json();

            // Populate form fields
            document.getElementById('script-name').value = script.name;
            document.getElementById('script-content').value = script.content;
            document.getElementById('interpreter').value = script.interpreter;
            document.getElementById('script-description').value = script.description || '';

            // Update execute button state
            updateExecuteButton();

            showToast(`Loaded script: ${script.name}`, 'success');
        } else {
            showToast('Failed to load script from library', 'danger');
        }
    } catch (error) {
        console.error('Error loading script:', error);
        showToast(`Error: ${error.message}`, 'danger');
    }
}

// =============================================================================
// Utility Functions
// =============================================================================

function showToast(message, type) {
    // Use the global showToast function if available (from base.html)
    if (window.showToast && window.showToast !== showToast) {
        window.showToast(message, type);
    } else {
        // Fallback to execution status area if global toast not available
        console.log(`[${type}] ${message}`);
        const statusDiv = document.getElementById('execution-status');
        if (statusDiv) {
            const alertClass = type === 'success' ? 'alert-success' :
                              type === 'warning' ? 'alert-warning' :
                              type === 'danger' ? 'alert-danger' : 'alert-info';
            statusDiv.innerHTML = `
                <div class="alert ${alertClass} alert-dismissible fade show" role="alert">
                    ${window.Utils.escapeHtml(message)}
                    <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
                </div>
            `;
        }
    }
}

// Export functions to global scope
window.testConnectivity = testConnectivity;
window.validateScript = validateScript;
window.executeScript = executeScript;
window.downloadResults = downloadResults;
window.loadFromLibrary = loadFromLibrary;

// Handle change updates from shared change management module
window.addEventListener('change-updated', function(e) {
    if (e.detail && e.detail.change) {
        currentChange = e.detail.change;
        if (instanceSelector) {
            instanceSelector.setInstances(currentChange.instances || []);
        }
        updateExecuteButton();
    }
});

})();
