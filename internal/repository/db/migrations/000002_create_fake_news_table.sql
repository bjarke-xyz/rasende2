-- +goose Up
create table if not exists fake_news(
    site_name text not null,
    title text not null,
    content text not null,
    published timestamp,
    primary key(site_name, title)
);

-- +goose Down
drop table if exists fake_news;