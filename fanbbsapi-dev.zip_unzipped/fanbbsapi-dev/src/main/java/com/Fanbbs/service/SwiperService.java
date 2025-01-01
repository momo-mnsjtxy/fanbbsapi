package com.Fanbbs.service;

import com.Fanbbs.common.PageList;
import com.Fanbbs.entity.Swiper;
import com.Fanbbs.entity.Swiper;
import org.springframework.stereotype.Component;

import java.util.List;

/**
* @author Amiya
* @description 操作Service
* @createDate 2024-03-31 20:28:16
*/

public interface SwiperService {
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
    int batchDelete(List<Object> keys);

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
    PageList<Swiper> selectPage (Swiper swiper, Integer page, Integer pageSize, String order);

    /**
     * [总量查询]
     **/
    int total(Swiper swiper);

}
