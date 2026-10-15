CREATE ROLE backup_ro WITH LOGIN PASSWORD 'Te:st\pa"ss#1';
GRANT pg_read_all_data TO backup_ro;
CREATE TABLE customers (id serial PRIMARY KEY, name text NOT NULL);
INSERT INTO customers (name) SELECT 'customer ' || g FROM generate_series(1, 1000) AS g;
