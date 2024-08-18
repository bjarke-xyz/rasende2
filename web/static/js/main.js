function handleCharts() {
    const chartsPlaceholder = document.getElementById('charts-placeholder')
    if (!chartsPlaceholder) {
        return;
    }
    const plugin = {
        id: "custom_canvas_background_color",
        beforeDraw: (chart) => {
            const { ctx } = chart;
            ctx.save();
            ctx.globalCompositeOperation = "destination-over";
            ctx.fillStyle = "#ffffff";
            ctx.fillRect(0, 0, chart.width, chart.height);
            ctx.restore();
        },
    };
    const makeOptions = (title) => {
        return {
            responsive: true,

            plugins: {
                legend: {
                    position: "top",
                },
                title: {
                    display: true,
                    text: title,
                },
            },
        };
    };
    const colors = [
        "#3366CC",
        "#DC3912",
        "#FF9900",
        "#109618",
        "#990099",
        "#3B3EAC",
        "#0099C6",
        "#DD4477",
        "#66AA00",
        "#B82E2E",
        "#316395",
        "#994499",
        "#22AA99",
        "#AAAA11",
        "#6633CC",
        "#E67300",
        "#8B0707",
        "#329262",
        "#5574A6",
        "#3B3EAC",
    ];
    const chartProps = JSON.parse(chartsPlaceholder.getAttribute('data-charts-json'))
    chartProps.charts.forEach((chart) => {
        if (chart.type === "doughnut" || chart.type === "pie") {
            chart.datasets[0].borderColor = [];
            chart.datasets[0].backgroundColor = [];
            chart.labels.forEach((label, i) => {
                if (Array.isArray(chart.datasets[0].borderColor)) {
                    chart.datasets[0].borderColor.push(colors[i % colors.length]);
                }
                if (Array.isArray(chart.datasets[0].backgroundColor)) {
                    chart.datasets[0].backgroundColor.push(colors[i % colors.length]);
                }
            });
        } else {
            chart.datasets.forEach((dataset, i) => {
                if (!dataset.borderColor && !dataset.backgroundColor) {
                    dataset.borderColor = colors[i];
                    dataset.backgroundColor = colors[i];
                }
            });
        }
        chart.data = {
            datasets: chart.datasets,
            labels: chart.labels
        }
        chart.options = {
            ...makeOptions(chart.title),
        }
        chart.plugins = [plugin]
    });
    chartsPlaceholder.innerHTML = '';
    for (const chartData of chartProps.charts) {
        const canvasContainer = document.createElement('div');
        canvasContainer.style.position = 'relative';
        canvasContainer.style.height = '400px';
        canvasContainer.style.width = '400px';
        const canvas = document.createElement('canvas');
        canvasContainer.appendChild(canvas);
        chartsPlaceholder.appendChild(canvasContainer);

        new Chart(canvas, chartData)
    }
}

async function main() {
    ready(() => {
        handleCharts();
    })
}


// utils
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


// main
main();
