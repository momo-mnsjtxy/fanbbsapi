package com.Fanbbs.service.impl;

import com.Fanbbs.common.PageList;
import com.Fanbbs.dao.ReportDao;
import com.Fanbbs.dao.ReportDao;
import com.Fanbbs.entity.Report;
import com.Fanbbs.service.ReportService;
import com.Fanbbs.service.ReportService;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;

import java.util.List;

/**
 * 业务层实现类
 * ReportServiceImpl
 *
 * @author report
 * @date 2022/09/06
 */
@Service
public class ReportServiceImpl implements ReportService {

    @Autowired
    ReportDao dao;

    @Override
    public int insert(Report report) {
        return dao.insert(report);
    }


    @Override
    public int update(Report report) {
        return dao.update(report);
    }

    @Override
    public int delete(Object key) {
        return dao.delete(key);
    }


    @Override
    public Report selectByKey(Object key) {
        return dao.selectByKey(key);
    }

    @Override
    public List<Report> selectList(Report report) {
        return dao.selectList(report);
    }

    @Override
    public PageList<Report> selectPage(Report report, Integer offset, Integer pageSize, String searchKey, String order) {
        PageList<Report> pageList = new PageList<>();

        int total = this.total(report);

        int totalPage;
        if (total % pageSize != 0) {
            totalPage = (total / pageSize) + 1;
        } else {
            totalPage = total / pageSize;
        }

        int page = (offset - 1) * pageSize;

        List<Report> list = dao.selectPage(report, page, pageSize, searchKey,order);

        pageList.setList(list);
        pageList.setStartPageNo(offset);
        pageList.setPageSize(pageSize);
        pageList.setTotalCount(total);
        pageList.setTotalPageCount(totalPage);
        return pageList;
    }

    @Override
    public int total(Report report) {
        return dao.total(report);
    }
}