// Shared attachment viewer: a modal that shows an uploaded file (image or
// PDF) inline with an obvious, single-click "✕ Close" — plus a separate
// Download action — instead of relying on the browser's own tab-closing to
// dismiss a view. Loaded on every page that lists uploaded attachments
// (admin/agent booking docs, Accounts/Legal review, Conveyancing, Mark
// Sold, Plots Overview). Self-contained, no dependencies; call
// openFileViewer(url, filename) from an attachment's "View" link.
(function () {
    function buildModal() {
        if (document.getElementById('fv-modal')) return;
        var modal = document.createElement('div');
        modal.id = 'fv-modal';
        modal.className = 'fv-modal';
        modal.innerHTML =
            '<div class="fv-modal-box">' +
                '<div class="fv-modal-header">' +
                    '<span class="fv-modal-title" id="fv-modal-title"></span>' +
                    '<div class="fv-modal-actions">' +
                        '<a id="fv-modal-download" class="fv-modal-download" href="#" download>⬇ Download</a>' +
                        '<button type="button" class="fv-modal-close" id="fv-modal-close-btn">✕ Close</button>' +
                    '</div>' +
                '</div>' +
                '<div class="fv-modal-body" id="fv-modal-body"></div>' +
            '</div>';
        document.body.appendChild(modal);
        modal.addEventListener('click', function (e) {
            if (e.target === modal) closeFileViewer();
        });
        document.getElementById('fv-modal-close-btn').addEventListener('click', closeFileViewer);
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Escape') closeFileViewer();
        });
    }

    window.openFileViewer = function (url, filename) {
        buildModal();
        document.getElementById('fv-modal-title').textContent = filename || 'File';
        var dl = document.getElementById('fv-modal-download');
        dl.href = url;
        dl.setAttribute('download', filename || '');

        var ext = (filename || url).split('.').pop().toLowerCase().split('?')[0];
        var isImage = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp'].indexOf(ext) !== -1;
        var isPDF = ext === 'pdf';
        var body = document.getElementById('fv-modal-body');
        if (isImage) {
            body.innerHTML = '<img src="' + url + '" alt="">';
        } else if (isPDF) {
            body.innerHTML = '<iframe src="' + url + '"></iframe>';
        } else {
            body.innerHTML =
                '<div style="padding:40px;text-align:center;color:#64748b;font-size:13px;">' +
                'Preview not available for this file type.<br><br>' +
                '<a href="' + url + '" target="_blank" style="color:#2563eb;font-weight:600;">Open in a new tab instead</a></div>';
        }
        document.getElementById('fv-modal').classList.add('open');
        document.body.style.overflow = 'hidden';
    };

    window.closeFileViewer = function () {
        var modal = document.getElementById('fv-modal');
        if (modal) modal.classList.remove('open');
        document.body.style.overflow = '';
    };
})();
