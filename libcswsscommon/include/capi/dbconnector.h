#ifndef _C_DBCONNECTOR_H
#define _C_DBCONNECTOR_H

#include <hiredis/hiredis.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef void *db_connector_t;

// DBConnector::DBConnector(int db, std::string hostname, int port, unsigned int timeout)
db_connector_t db_connector_new(int db, const char *hostname, int port, unsigned int timeout);
// DBConnector::DBConnector(int db, std::string unixPath, unsigned int timeout)
db_connector_t db_connector_new2(int db, const char *unixPath, unsigned int timeout);

// DBConnector::~DBConnector()
void db_connector_delete(db_connector_t db);

// redisContext *DBConnector::getContext()
redisContext *db_connector_get_context(db_connector_t db);
// int DBConnector::getDB()
int db_connector_get_db(db_connector_t db);

// static void DBConnector::select(DBConnector *db)
void db_connector_select(db_connector_t db);

// DBConnector *DBConnector::newConnector(unsigned int timeout);
db_connector_t db_connector_new_connector(db_connector_t db, unsigned int timeout);

#ifdef __cplusplus
}
#endif

#endif
