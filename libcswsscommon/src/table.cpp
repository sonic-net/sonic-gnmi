#include <capi/table.h>
#include <table.h>
#include <dbconnector.h>
#include <redispipeline.h>

#include <string>
#include <vector>
#include <tuple>

table_t table_new(db_connector_t db, const char *tableName)
{
    auto pt = new swss::Table(static_cast<swss::DBConnector*>(db), std::string(tableName));
    return static_cast<table_t>(pt);
}

table_t table_new2(redis_pipeline_t pipeline, const char *tableName, bool buffered)
{
    auto pt = new swss::Table(static_cast<swss::RedisPipeline*>(pipeline), std::string(tableName), buffered);
    return static_cast<table_t>(pt);
}

void table_delete(table_t pt)
{
    delete static_cast<swss::Table*>(pt);
}

void table_set_buffered(table_t pt, bool buffered)
{
    static_cast<swss::Table*>(pt)->setBuffered(buffered);
}

void table_set(table_t pt,
                        const char *key,
                        const field_value_tuple_t *values,
                        size_t count,
                        const char *op,
                        const char *prefix)
{
    std::vector<swss::FieldValueTuple> tuples;
    for(size_t i = 0; i < count; i++)
    {
        auto tuple = std::make_pair(std::string(values[i].field), std::string(values[i].value));
        tuples.push_back(tuple);
    }
    static_cast<swss::Table*>(pt)->set(std::string(key), tuples, std::string(op), std::string(prefix));
}

void table_del(table_t pt,
                        const char *key,
                        const char *op,
                        const char *prefix)
{
    static_cast<swss::Table*>(pt)->del(std::string(key), std::string(op), std::string(prefix));
}

void table_flush(table_t pt)
{
    static_cast<swss::Table*>(pt)->flush();
}
