package com.Fanbbs.dao;

import com.Fanbbs.entity.*;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

import java.util.List;

@Mapper
public interface ReportDao {

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
    List<Report> selectPage(@Param("report") Report report, @Param("page") Integer page, @Param("pageSize") Integer pageSize, @Param("searchKey") String searchKey, @Param("order") String order);

    /**
     * [总量查询]
     **/
    int total(Report report);
}
