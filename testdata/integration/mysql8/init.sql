CREATE USER 'backup_ro'@'%' IDENTIFIED BY 'Te:st\\pa"ss#1';
GRANT SELECT, SHOW VIEW, TRIGGER, LOCK TABLES, EVENT ON `app_prod`.* TO 'backup_ro'@'%';
GRANT SHOW_ROUTINE ON *.* TO 'backup_ro'@'%';
USE app_prod;
CREATE TABLE customers (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL);
INSERT INTO customers (name) VALUES ('alpha'), ('beta'), ('gamma');
