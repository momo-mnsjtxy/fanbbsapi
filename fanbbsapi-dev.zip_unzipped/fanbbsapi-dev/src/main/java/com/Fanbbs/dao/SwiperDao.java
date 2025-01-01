package com.Fanbbs.dao;

import com.Fanbbs.entity.*;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

import java.util.List;
@Mapper
public interface SwiperDao {
    /**
     * [新增]
     **/
    int insert(Swiper swiper);

    /**
     * [批量新增]
     **/
    int batchInsert(List<Swiper> list);

    /**
     * [更新]
     **/
    int update(Swiper swiper);

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
    Swiper selectByKey(Object key);

    /**
     * [条件查询]
     **/
    List<Swiper> selectList (Swiper swiper);

    /**
     * [分页条件查询]
     **/
    List<Swiper> selectPage (@Param("swiper") Swiper swiper, @Param("page") Integer page, @Param("pageSize") Integer pageSize, @Param("order") String order);

    /**
     * [总量查询]
     **/
    int total(Swiper swiper);
}
