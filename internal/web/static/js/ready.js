
function ready(fn) {
    if (typeof fn !== 'function') {
        throw new Error('Argument passed to ready should be a function');
    }

    if (document.readyState != 'loading') {
        fn();
    } else if (document.addEventListener) {
        document.addEventListener('DOMContentLoaded', fn, {
            once: true // A boolean value indicating that the listener should be invoked at most once after being added. If true, the listener would be automatically removed when invoked.
        });
    } else {
        document.attachEvent('onreadystatechange', function () {
            if (document.readyState != 'loading')
                fn();
        });
    }
}