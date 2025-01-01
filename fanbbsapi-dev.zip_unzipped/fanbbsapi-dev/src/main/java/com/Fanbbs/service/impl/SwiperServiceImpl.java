package com.Fanbbs.service.impl;

import com.Fanbbs.entity.*;
import com.Fanbbs.common.PageList;
import com.Fanbbs.dao.*;
import com.Fanbbs.service.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;

import java.util.List;

/**
 * 业务层实现类
 * SwiperServiceImpl
 *
 * @author swiper
 * @date 2023/07/10
 */
@Service
public class SwiperServiceImpl implements SwiperService {

    @Autowired
    SwiperDao dao;

    @Override
    public int insert(Swiper swiper) {
        return dao.insert(swiper);
    }

    @Override
    public int batchInsert(List<Swiper> list) {
        return dao.batchInsert(list);
    }

    @Override
    public int update(Swiper swiper) {
        return dao.update(swiper);
    }

    @Override
    public int delete(Object key) {
        return dao.delete(key);
    }

    @Override
    public int batchDelete(List<Object> keys) {
        return dao.batchDelete(keys);
    }

    @Override
    public Swiper selectByKey(Object key) {
        return dao.selectByKey(key);
    }

    @Override
    public List<Swiper> selectList(Swiper swiper) {
        return dao.selectList(swiper);
    }

    @Override
    public PageList<Swiper> selectPage(Swiper swiper, Integer offset, Integer pageSize, String order) {
        PageList<Swiper> pageList = new PageList<>();

        int total = this.total(swiper);

        int totalPage;
        if (total % pageSize != 0) {
            totalPage = (total / pageSize) + 1;
        } else {
            totalPage = total / pageSize;
        }

        int page = (offset - 1) * pageSize;

        List<Swiper> list = dao.selectPage(swiper, page, pageSize, order);

        pageList.setList(list);
        pageList.setStartPageNo(offset);
        pageList.setPageSize(pageSize);
        pageList.setTotalCount(total);
        pageList.setTotalPageCount(totalPage);
        return pageList;
    }

    @Override
    public int total(Swiper swiper) {
        return dao.total(swiper);
    }
}