package com.Fanbbs.service;

import com.Fanbbs.common.PageList;
import com.Fanbbs.entity.Report;

import java.util.List;

public interface ReportService {

    /**
     * [新增]
     **/
    int insert(Report report);

    /**
     * [更新]
     **/
    int update(Report report);

    /**
     * [删除]
     **/
    int delete(Object key);

    /**
     * [主键查询]
     **/
    Report selectByKey(Object key);

    /**
     * [条件查询]
     **/
    List<Report> selectList(Report report);

    /**
     * [分页条件查询]
     **/
    PageList<Report> selectPage(Report report, Integer page, Integer pageSize, String searchKey, String order);

    /**
     * [总量查询]
     **/
    int total(Report report);
}
