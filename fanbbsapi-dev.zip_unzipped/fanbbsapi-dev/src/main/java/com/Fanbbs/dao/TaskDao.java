package com.Fanbbs.dao;

import com.Fanbbs.entity.Task;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

import java.util.List;

@Mapper
public interface TaskDao {
    /**
     * [新增]
     **/
    int insert(Task task);

    /**
     * [批量新增]
     **/
    int batchInsert(List<Task> list);

    /**
     * [更新]
     **/
    int update(Task task);

    /**
     * [删除]
     **/
    int delete(Object key);

    /**
     * [批量删除]
     **/
    int batchDelete(List<Object> list);

    /**
     * [主键查询]
     **/
    Task selectByKey(Object key);

    /**
     * [条件查询]
     **/
    List<Task> selectList (Task task);

    /**
     * [分页条件查询]
     **/
    List<Task> selectPage (@Param("task") Task task, @Param("page") Integer page, @Param("pageSize") Integer pageSize, @Param("order") String order);

    /**
     * [总量查询]
     **/
    int total(Task task);
}
