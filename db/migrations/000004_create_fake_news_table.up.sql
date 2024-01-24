CREATE TABLE IF NOT EXISTS fake_news (
    site_name VARCHAR(255) NOT NULL,
    title VARCHAR(400) NOT NULL,
    content TEXT NOT NULL,
    published TIMESTAMP,
    PRIMARY KEY (site_name, title)
);