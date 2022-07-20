#include <capi/dbconnector.h>
#include <dbconnector.h>

#include <string>

extern "C" db_connector_t db_connector_new(int db, const char *hostname, int port, unsigned int timeout)
{
    auto dbc = new swss::DBConnector(db, std::string(hostname), port, timeout);
    return static_cast<db_connector_t>(dbc);
}

extern "C" db_connector_t db_connector_new2(int db, const char *unixPath, unsigned int timeout)
{
    auto dbc = new swss::DBConnector(db, std::string(unixPath), timeout);
    return static_cast<db_connector_t>(dbc);
}

extern "C" void db_connector_delete(db_connector_t db)
{
    delete static_cast<swss::DBConnector*>(db);
}

extern "C" redisContext *db_connector_get_context(db_connector_t db)
{
    return static_cast<swss::DBConnector*>(db)->getContext();
}

extern "C" int db_connector_get_db(db_connector_t db)
{
    return static_cast<swss::DBConnector*>(db)->getDbId();
}

extern "C" void db_connector_select(db_connector_t db)
{
    swss::DBConnector::select(static_cast<swss::DBConnector*>(db));
}

extern "C" db_connector_t db_connector_new_connector(db_connector_t db, unsigned int timeout)
{
    auto dbc = static_cast<swss::DBConnector*>(db)->newConnector(timeout);
    return static_cast<db_connector_t>(dbc);
}
