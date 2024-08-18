# Rasende2

Scrape nyhedssider RSS, find titler der indeholder ordet rasende. 

## Database notes

Currently using SQLite and [bleve](https://github.com/blevesearch/bleve) for search

Before that, PostgreSQL with `to_tsvector('danish', ...)`.

MySQL was tried on PlanetScale in january 2024, but MySQL's `FULLTEXT` index did not give "correct" results.  
PostgreSQL found the verb tenses of `rasende`, but MySQL did not.


