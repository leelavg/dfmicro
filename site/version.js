(() => {
    const addVersionSelector = () => {
        const version = document.getElementById("dfmicro-version");
        const toolbar = document.querySelector("#mdbook-menu-bar .right-buttons");
        if (!version || !toolbar) return;

        toolbar.append(version);
        version.classList.add("dfmicro-ready");
    };

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", addVersionSelector, { once: true });
    } else {
        addVersionSelector();
    }
})();
