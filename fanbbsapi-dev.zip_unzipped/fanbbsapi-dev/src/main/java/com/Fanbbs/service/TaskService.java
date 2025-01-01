package com.Fanbbs.service;

import com.Fanbbs.common.PageList;
import com.Fanbbs.entity.Task;

import java.util.List;

public interface TaskService {
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
    int batchDelete(List<Object> keys);

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
    PageList<Task> selectPage (Task task, Integer page, Integer pageSize, String order);

    /**
     * [总量查询]
     **/
    int total(Task task);
}
